package restore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/influxdata/influxdb/bolt"
	"github.com/influxdata/influxdb/cmd/influxd/inspect"
	"github.com/influxdata/influxdb/internal/fs"
	"github.com/influxdata/influxdb/kit/cli"
	"github.com/influxdata/influxdb/storage"
	"github.com/spf13/cobra"
)

var Command = &cobra.Command{
	Use:   "restore",
	Short: "Restore data and metadata from a backup",
	Long: `
This command restores data and metadata from a backup fileset.

Any existing metadata and data will be temporarily moved while restore runs
and deleted after restore completes.

Rebuilding the index and series file uses default options as in
"influxd inspect build-tsi" with the given target engine path.
For additional performance options, run restore with "-rebuild-index false"
and build-tsi afterwards.

NOTES:

* The influxd server should not be running when using the restore tool
  as it replaces all data and metadata.
`,
	Args: cobra.ExactArgs(0),
	RunE: restoreE,
}

var flags struct {
	boltPath   string
	enginePath string
	backupPath string
	rebuildTSI bool
}

func init() {
	dir, err := fs.InfluxDir()
	if err != nil {
		panic(fmt.Errorf("failed to determine influx directory: %v", err))
	}

	Command.Flags().SortFlags = false

	pfs := Command.PersistentFlags()
	pfs.SortFlags = false

	opts := []cli.Opt{
		{
			DestP:   &flags.boltPath,
			Flag:    "bolt-path",
			Default: filepath.Join(dir, bolt.DefaultFilename),
			Desc:    "path to target boltdb database",
		},
		{
			DestP:   &flags.enginePath,
			Flag:    "engine-path",
			Default: filepath.Join(dir, "engine"),
			Desc:    "path to target persistent engine files",
		},
		{
			DestP:   &flags.backupPath,
			Flag:    "backup-path",
			Default: "",
			Desc:    "path to backup files",
		},
		{
			DestP:   &flags.rebuildTSI,
			Flag:    "rebuild-index",
			Default: true,
			Desc:    "if true, rebuild the TSI index and series file based on the given engine path (equivalent to influxd inspect build-tsi)",
		},
	}

	cli.BindOptions(Command, opts)
}

func restoreE(cmd *cobra.Command, args []string) error {
	if flags.backupPath == "" {
		return fmt.Errorf("no backup path given")
	}

	if err := moveBolt(); err != nil {
		return fmt.Errorf("failed to move existing bolt file: %v", err)
	}

	if err := moveEngine(); err != nil {
		return fmt.Errorf("failed to move existing engine data: %v", err)
	}

	if err := restoreBolt(); err != nil {
		return fmt.Errorf("failed to restore bolt file: %v", err)
	}

	if err := restoreEngine(); err != nil {
		return fmt.Errorf("failed to restore all TSM files: %v", err)
	}

	if flags.rebuildTSI {
		sFilePath := filepath.Join(flags.enginePath, storage.DefaultSeriesFileDirectoryName)
		indexPath := filepath.Join(flags.enginePath, storage.DefaultIndexDirectoryName)

		rebuild := inspect.NewBuildTSICommand()
		rebuild.SetArgs([]string{"--sfile-path", sFilePath, "--tsi-path", indexPath})
		rebuild.Execute()
	}

	if err := removeTmpBolt(); err != nil {
		return fmt.Errorf("restore completed, but failed to cleanup temporary bolt file: %v", err)
	}

	if err := removeTmpEngine(); err != nil {
		return fmt.Errorf("restore completed, but failed to cleanup temporary engine data: %v", err)
	}

	return nil
}

func moveBolt() error {
	if _, err := os.Stat(flags.boltPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	if err := removeTmpBolt(); err != nil {
		return err
	}

	return os.Rename(flags.boltPath, flags.boltPath+".tmp")
}

func moveEngine() error {
	if _, err := os.Stat(flags.enginePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	if err := removeTmpEngine(); err != nil {
		return err
	}

	if err := os.Rename(flags.enginePath, tmpEnginePath()); err != nil {
		return err
	}

	return os.Mkdir(flags.enginePath, 0777)
}

func tmpEnginePath() string {
	return filepath.Dir(flags.enginePath) + "tmp"
}

func removeTmpBolt() error {
	return removeIfExists(flags.boltPath + ".tmp")
}

func removeTmpEngine() error {
	return removeIfExists(tmpEnginePath())
}

func removeIfExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	} else {
		return os.RemoveAll(path)
	}
}

func restoreBolt() error {
	backupBolt := filepath.Join(flags.backupPath, bolt.DefaultFilename)
	f, err := os.OpenFile(backupBolt, os.O_RDONLY, 0666)
	if err != nil {
		return fmt.Errorf("no bolt file in backup: %v", err)
	}
	defer f.Close()

	w, err := os.OpenFile(flags.boltPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	defer w.Close()

	_, err = io.Copy(w, f)
	if err != nil {
		return err
	}

	fmt.Printf("Restored Bolt to %s from %s\n", flags.boltPath, backupBolt)
	return nil
}

func restoreEngine() error {
	dataDir := filepath.Join(flags.enginePath, "/data")
	if err := os.Mkdir(dataDir, 0777); err != nil {
		return err
	}

	count := 0
	err := filepath.Walk(flags.backupPath, func(path string, info os.FileInfo, err error) error {
		if strings.Contains(path, ".tsm") {
			f, err := os.OpenFile(path, os.O_RDONLY, 0600)
			if err != nil {
				return fmt.Errorf("error opening TSM file: %v", err)
			}
			defer f.Close()

			tsmPath := filepath.Join(dataDir, filepath.Base(path))
			w, err := os.OpenFile(tsmPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0660)
			if err != nil {
				return err
			}

			_, err = io.Copy(w, f)
			if err != nil {
				return err
			}
			count++
			return nil
		}
		return nil
	})
	fmt.Printf("Restored %d TSM files to %v\n", count, dataDir)
	return err
}
