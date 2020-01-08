package endpoint

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/influxdata/influxdb"
)

var _ influxdb.NotificationEndpoint = &Slack{}

const slackTokenSuffix = "-token"

// Slack is the notification endpoint config of slack.
type Slack struct {
	influxdb.EndpointBase
	// URL is a valid slack webhook URL
	// TODO(jm): validate this in unmarshaler
	// example: https://slack.com/api/chat.postMessage
	URL string `json:"url"`
	// Token is the bearer token for authorization
	Token influxdb.SecretField `json:"token"`
}

func (s *Slack) Base() *influxdb.EndpointBase {
	return &s.EndpointBase
}

// BackfillSecretKeys fill back fill the secret field key during the unmarshalling
// if value of that secret field is not nil.
func (s *Slack) BackfillSecretKeys() {
	if s.Token.Key == "" && s.Token.Value != nil {
		s.Token.Key = s.ID.String() + slackTokenSuffix
	}
}

// SecretFields return available secret fields.
func (s Slack) SecretFields() []influxdb.SecretField {
	arr := []influxdb.SecretField{}
	if s.Token.Key != "" {
		arr = append(arr, s.Token)
	}
	return arr
}

// Valid returns error if some configuration is invalid
func (s Slack) Valid() error {
	if err := s.EndpointBase.Valid(); err != nil {
		return err
	}
	if s.URL == "" {
		return &influxdb.Error{
			Code: influxdb.EInvalid,
			Msg:  "slack endpoint URL must be provided",
		}
	}
	if s.URL != "" {
		if _, err := url.Parse(s.URL); err != nil {
			return &influxdb.Error{
				Code: influxdb.EInvalid,
				Msg:  fmt.Sprintf("slack endpoint URL is invalid: %s", err.Error()),
			}
		}
	}
	return nil
}

type slackAlias Slack

// MarshalJSON implement json.Marshaler interface.
func (s Slack) MarshalJSON() ([]byte, error) {
	return json.Marshal(
		struct {
			slackAlias
			Type string `json:"type"`
		}{
			slackAlias: slackAlias(s),
			Type:       s.Type(),
		})
}

// Type returns the type.
func (s Slack) Type() string {
	return SlackType
}
