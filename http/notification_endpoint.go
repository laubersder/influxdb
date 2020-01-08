package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/influxdata/httprouter"
	"github.com/influxdata/influxdb"
	pctx "github.com/influxdata/influxdb/context"
	"github.com/influxdata/influxdb/endpoints"
	"github.com/influxdata/influxdb/notification/endpoint"
	"github.com/influxdata/influxdb/pkg/httpc"
	"go.uber.org/zap"
)

// NotificationEndpointBackend is all services and associated parameters required to construct
// the NotificationEndpointBackendHandler.
type NotificationEndpointBackend struct {
	influxdb.HTTPErrorHandler
	log *zap.Logger

	NotificationEndpointService influxdb.NotificationEndpointService
	UserResourceMappingService  influxdb.UserResourceMappingService
	LabelService                influxdb.LabelService
	UserService                 influxdb.UserService
	OrganizationService         influxdb.OrganizationService
}

// NewNotificationEndpointBackend returns a new instance of NotificationEndpointBackend.
func NewNotificationEndpointBackend(log *zap.Logger, b *APIBackend) *NotificationEndpointBackend {
	return &NotificationEndpointBackend{
		HTTPErrorHandler: b.HTTPErrorHandler,
		log:              log,

		NotificationEndpointService: b.NotificationEndpointService,
		UserResourceMappingService:  b.UserResourceMappingService,
		LabelService:                b.LabelService,
		UserService:                 b.UserService,
		OrganizationService:         b.OrganizationService,
	}
}

func (b *NotificationEndpointBackend) Logger() *zap.Logger {
	return b.log
}

// NotificationEndpointHandler is the handler for the notificationEndpoint service
type NotificationEndpointHandler struct {
	*httprouter.Router
	influxdb.HTTPErrorHandler
	log *zap.Logger

	NotificationEndpointService influxdb.NotificationEndpointService
	LabelService                influxdb.LabelService
}

const (
	prefixNotificationEndpoints          = "/api/v2/notificationEndpoints"
	notificationEndpointsIDPath          = "/api/v2/notificationEndpoints/:id"
	notificationEndpointsIDMembersPath   = "/api/v2/notificationEndpoints/:id/members"
	notificationEndpointsIDMembersIDPath = "/api/v2/notificationEndpoints/:id/members/:userID"
	notificationEndpointsIDOwnersPath    = "/api/v2/notificationEndpoints/:id/owners"
	notificationEndpointsIDOwnersIDPath  = "/api/v2/notificationEndpoints/:id/owners/:userID"
	notificationEndpointsIDLabelsPath    = "/api/v2/notificationEndpoints/:id/labels"
	notificationEndpointsIDLabelsIDPath  = "/api/v2/notificationEndpoints/:id/labels/:lid"
)

// NewNotificationEndpointHandler returns a new instance of NotificationEndpointHandler.
func NewNotificationEndpointHandler(log *zap.Logger, b *NotificationEndpointBackend) *NotificationEndpointHandler {
	h := &NotificationEndpointHandler{
		Router:           NewRouter(b.HTTPErrorHandler),
		HTTPErrorHandler: b.HTTPErrorHandler,
		log:              log,

		NotificationEndpointService: b.NotificationEndpointService,
		LabelService:                b.LabelService,
	}
	h.HandlerFunc("POST", prefixNotificationEndpoints, h.handlePostNotificationEndpoint)
	h.HandlerFunc("GET", prefixNotificationEndpoints, h.handleGetNotificationEndpoints)
	h.HandlerFunc("GET", notificationEndpointsIDPath, h.handleGetNotificationEndpoint)
	h.HandlerFunc("DELETE", notificationEndpointsIDPath, h.handleDeleteNotificationEndpoint)
	h.HandlerFunc("PUT", notificationEndpointsIDPath, h.handlePutNotificationEndpoint)
	h.HandlerFunc("PATCH", notificationEndpointsIDPath, h.handlePatchNotificationEndpoint)

	memberBackend := MemberBackend{
		HTTPErrorHandler:           b.HTTPErrorHandler,
		log:                        b.log.With(zap.String("handler", "member")),
		ResourceType:               influxdb.NotificationEndpointResourceType,
		UserType:                   influxdb.Member,
		UserResourceMappingService: b.UserResourceMappingService,
		UserService:                b.UserService,
	}
	h.HandlerFunc("POST", notificationEndpointsIDMembersPath, newPostMemberHandler(memberBackend))
	h.HandlerFunc("GET", notificationEndpointsIDMembersPath, newGetMembersHandler(memberBackend))
	h.HandlerFunc("DELETE", notificationEndpointsIDMembersIDPath, newDeleteMemberHandler(memberBackend))

	ownerBackend := MemberBackend{
		HTTPErrorHandler:           b.HTTPErrorHandler,
		log:                        b.log.With(zap.String("handler", "member")),
		ResourceType:               influxdb.NotificationEndpointResourceType,
		UserType:                   influxdb.Owner,
		UserResourceMappingService: b.UserResourceMappingService,
		UserService:                b.UserService,
	}
	h.HandlerFunc("POST", notificationEndpointsIDOwnersPath, newPostMemberHandler(ownerBackend))
	h.HandlerFunc("GET", notificationEndpointsIDOwnersPath, newGetMembersHandler(ownerBackend))
	h.HandlerFunc("DELETE", notificationEndpointsIDOwnersIDPath, newDeleteMemberHandler(ownerBackend))

	labelBackend := &LabelBackend{
		HTTPErrorHandler: b.HTTPErrorHandler,
		log:              b.log.With(zap.String("handler", "label")),
		LabelService:     b.LabelService,
		ResourceType:     influxdb.TelegrafsResourceType,
	}
	h.HandlerFunc("GET", notificationEndpointsIDLabelsPath, newGetLabelsHandler(labelBackend))
	h.HandlerFunc("POST", notificationEndpointsIDLabelsPath, newPostLabelHandler(labelBackend))
	h.HandlerFunc("DELETE", notificationEndpointsIDLabelsIDPath, newDeleteLabelHandler(labelBackend))

	return h
}

type notificationEndpointLinks struct {
	Self    string `json:"self"`
	Labels  string `json:"labels"`
	Members string `json:"members"`
	Owners  string `json:"owners"`
}

type postNotificationEndpointRequest struct {
	influxdb.NotificationEndpoint
	Labels []string `json:"labels"`
}

type notificationEndpointResponse struct {
	influxdb.NotificationEndpoint
	Labels []influxdb.Label          `json:"labels"`
	Links  notificationEndpointLinks `json:"links"`
}

func (resp notificationEndpointResponse) MarshalJSON() ([]byte, error) {
	b1, err := json.Marshal(resp.NotificationEndpoint)
	if err != nil {
		return nil, err
	}

	b2, err := json.Marshal(struct {
		Labels []influxdb.Label          `json:"labels"`
		Links  notificationEndpointLinks `json:"links"`
	}{
		Links:  resp.Links,
		Labels: resp.Labels,
	})
	if err != nil {
		return nil, err
	}

	return []byte(string(b1[:len(b1)-1]) + ", " + string(b2[1:])), nil
}

type notificationEndpointsResponse struct {
	NotificationEndpoints []notificationEndpointResponse `json:"notificationEndpoints"`
	Links                 *influxdb.PagingLinks          `json:"links"`
}

func newNotificationEndpointResponse(edp influxdb.NotificationEndpoint, labels []*influxdb.Label) notificationEndpointResponse {
	res := notificationEndpointResponse{
		NotificationEndpoint: edp,
		Links: notificationEndpointLinks{
			Self:    fmt.Sprintf("/api/v2/notificationEndpoints/%s", edp.Base().ID),
			Labels:  fmt.Sprintf("/api/v2/notificationEndpoints/%s/labels", edp.Base().ID),
			Members: fmt.Sprintf("/api/v2/notificationEndpoints/%s/members", edp.Base().ID),
			Owners:  fmt.Sprintf("/api/v2/notificationEndpoints/%s/owners", edp.Base().ID),
		},
		Labels: []influxdb.Label{},
	}

	for _, l := range labels {
		res.Labels = append(res.Labels, *l)
	}

	return res
}

func newNotificationEndpointsResponse(ctx context.Context, edps []influxdb.NotificationEndpoint, labelService influxdb.LabelService, f influxdb.PagingFilter, opts influxdb.FindOptions) *notificationEndpointsResponse {
	resp := &notificationEndpointsResponse{
		NotificationEndpoints: make([]notificationEndpointResponse, len(edps)),
		Links:                 newPagingLinks(prefixNotificationEndpoints, opts, f, len(edps)),
	}
	for i, edp := range edps {
		labels, _ := labelService.FindResourceLabels(ctx, influxdb.LabelMappingFilter{ResourceID: edp.Base().ID})
		resp.NotificationEndpoints[i] = newNotificationEndpointResponse(edp, labels)
	}
	return resp
}

func decodeGetNotificationEndpointRequest(ctx context.Context) (i influxdb.ID, err error) {
	params := httprouter.ParamsFromContext(ctx)
	id := params.ByName("id")
	if id == "" {
		return i, &influxdb.Error{
			Code: influxdb.EInvalid,
			Msg:  "url missing id",
		}
	}

	if err := i.DecodeFromString(id); err != nil {
		return i, err
	}
	return i, nil
}

func (h *NotificationEndpointHandler) handleGetNotificationEndpoints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter, opts, err := decodeNotificationEndpointFilter(ctx, r)
	if err != nil {
		h.log.Debug("Failed to decode request", zap.Error(err))
		h.HandleHTTPError(ctx, err, w)
		return
	}
	edps, err := h.NotificationEndpointService.Find(ctx, filter, opts)
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}
	h.log.Debug("NotificationEndpoints retrieved", zap.String("notificationEndpoints", fmt.Sprint(edps)))

	if err := encodeResponse(ctx, w, http.StatusOK, newNotificationEndpointsResponse(ctx, edps, h.LabelService, filter, opts)); err != nil {
		logEncodingError(h.log, r, err)
		return
	}
}

func (h *NotificationEndpointHandler) handleGetNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := decodeGetNotificationEndpointRequest(ctx)
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}
	edp, err := h.NotificationEndpointService.FindByID(ctx, id)
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}
	h.log.Debug("NotificationEndpoint retrieved", zap.String("notificationEndpoint", fmt.Sprint(edp)))

	labels, err := h.LabelService.FindResourceLabels(ctx, influxdb.LabelMappingFilter{ResourceID: edp.Base().ID})
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	if err := encodeResponse(ctx, w, http.StatusOK, newNotificationEndpointResponse(edp, labels)); err != nil {
		logEncodingError(h.log, r, err)
		return
	}
}

func decodeNotificationEndpointFilter(ctx context.Context, r *http.Request) (influxdb.NotificationEndpointFilter, influxdb.FindOptions, error) {
	f := influxdb.NotificationEndpointFilter{
		UserResourceMappingFilter: influxdb.UserResourceMappingFilter{
			ResourceType: influxdb.NotificationEndpointResourceType,
		},
	}

	opts, err := decodeFindOptions(ctx, r)
	if err != nil {
		return influxdb.NotificationEndpointFilter{}, influxdb.FindOptions{}, err
	}

	q := r.URL.Query()
	if orgIDStr := q.Get("orgID"); orgIDStr != "" {
		orgID, err := influxdb.IDFromString(orgIDStr)
		if err != nil {
			return influxdb.NotificationEndpointFilter{}, influxdb.FindOptions{}, &influxdb.Error{
				Code: influxdb.EInvalid,
				Msg:  "orgID is invalid",
				Err:  err,
			}
		}
		f.OrgID = orgID
	} else if orgNameStr := q.Get("org"); orgNameStr != "" {
		*f.Org = orgNameStr
	}

	if userID := q.Get("user"); userID != "" {
		id, err := influxdb.IDFromString(userID)
		if err != nil {
			return influxdb.NotificationEndpointFilter{}, influxdb.FindOptions{}, err
		}
		f.UserID = *id
	}

	return f, *opts, err
}

func decodePostNotificationEndpointRequest(r *http.Request) (postNotificationEndpointRequest, error) {
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return postNotificationEndpointRequest{}, &influxdb.Error{
			Code: influxdb.EInvalid,
			Err:  err,
		}
	}
	defer r.Body.Close()
	edp, err := endpoint.UnmarshalJSON(b)
	if err != nil {
		return postNotificationEndpointRequest{}, &influxdb.Error{
			Code: influxdb.EInvalid,
			Err:  err,
		}
	}

	var dl decodeLabels
	if err := json.Unmarshal(b, &dl); err != nil {
		return postNotificationEndpointRequest{}, &influxdb.Error{
			Code: influxdb.EInvalid,
			Err:  err,
		}
	}

	return postNotificationEndpointRequest{
		NotificationEndpoint: edp,
		Labels:               dl.Labels,
	}, nil
}

func decodePutNotificationEndpointRequest(ctx context.Context, r *http.Request) (influxdb.NotificationEndpoint, error) {
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		return nil, &influxdb.Error{
			Code: influxdb.EInvalid,
			Err:  err,
		}
	}
	defer r.Body.Close()

	edp, err := endpoint.UnmarshalJSON(buf.Bytes())
	if err != nil {
		return nil, &influxdb.Error{
			Code: influxdb.EInvalid,
			Err:  err,
		}
	}

	params := httprouter.ParamsFromContext(ctx)
	i, err := influxdb.IDFromString(params.ByName("id"))
	if err != nil {
		return nil, err
	}
	edp.Base().ID = *i
	return edp, nil
}

type patchNotificationEndpointRequest struct {
	influxdb.ID
	Update influxdb.NotificationEndpointUpdate
}

func decodePatchNotificationEndpointRequest(ctx context.Context, r *http.Request) (patchNotificationEndpointRequest, error) {
	params := httprouter.ParamsFromContext(ctx)
	id, err := influxdb.IDFromString(params.ByName("id"))
	if err != nil {
		return patchNotificationEndpointRequest{}, err
	}
	req := patchNotificationEndpointRequest{
		ID: *id,
	}

	var upd influxdb.NotificationEndpointUpdate
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		return patchNotificationEndpointRequest{}, &influxdb.Error{
			Code: influxdb.EInvalid,
			Msg:  err.Error(),
		}
	}
	if err := upd.Valid(); err != nil {
		return patchNotificationEndpointRequest{}, &influxdb.Error{
			Code: influxdb.EInvalid,
			Msg:  err.Error(),
		}
	}

	req.Update = upd
	return req, nil
}

// handlePostNotificationEndpoint is the HTTP handler for the POST /api/v2/notificationEndpoints route.
func (h *NotificationEndpointHandler) handlePostNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	edp, err := decodePostNotificationEndpointRequest(r)
	if err != nil {
		h.log.Debug("Failed to decode request", zap.Error(err))
		h.HandleHTTPError(ctx, err, w)
		return
	}

	auth, err := pctx.GetAuthorizer(ctx)
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	err = h.NotificationEndpointService.Create(ctx, auth.GetUserID(), edp.NotificationEndpoint)
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	labels := h.mapNewNotificationEndpointLabels(ctx, edp.NotificationEndpoint, edp.Labels)

	h.log.Debug("NotificationEndpoint created", zap.String("notificationEndpoint", fmt.Sprint(edp)))

	if err := encodeResponse(ctx, w, http.StatusCreated, newNotificationEndpointResponse(edp, labels)); err != nil {
		logEncodingError(h.log, r, err)
		return
	}
}

func (h *NotificationEndpointHandler) mapNewNotificationEndpointLabels(ctx context.Context, nre influxdb.NotificationEndpoint, labels []string) []*influxdb.Label {
	var ls []*influxdb.Label
	for _, sid := range labels {
		var lid influxdb.ID
		err := lid.DecodeFromString(sid)

		if err != nil {
			continue
		}

		label, err := h.LabelService.FindLabelByID(ctx, lid)
		if err != nil {
			continue
		}

		mapping := influxdb.LabelMapping{
			LabelID:      label.ID,
			ResourceID:   nre.Base().ID,
			ResourceType: influxdb.NotificationEndpointResourceType,
		}

		err = h.LabelService.CreateLabelMapping(ctx, &mapping)
		if err != nil {
			continue
		}

		ls = append(ls, label)
	}
	return ls
}

// handlePutNotificationEndpoint is the HTTP handler for the PUT /api/v2/notificationEndpoints route.
func (h *NotificationEndpointHandler) handlePutNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	edp, err := decodePutNotificationEndpointRequest(ctx, r)
	if err != nil {
		h.log.Debug("Failed to decode request", zap.Error(err))
		h.HandleHTTPError(ctx, err, w)
		return
	}

	edp, err = h.NotificationEndpointService.Update(ctx, endpoints.UpdateEndpoint(edp))
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	labels, err := h.LabelService.FindResourceLabels(ctx, influxdb.LabelMappingFilter{
		ResourceID: edp.Base().ID,
	})
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}
	h.log.Debug("NotificationEndpoint replaced", zap.String("notificationEndpoint", fmt.Sprint(edp)))

	if err := encodeResponse(ctx, w, http.StatusOK, newNotificationEndpointResponse(edp, labels)); err != nil {
		logEncodingError(h.log, r, err)
		return
	}
}

// handlePatchNotificationEndpoint is the HTTP handler for the PATCH /api/v2/notificationEndpoints/:id route.
func (h *NotificationEndpointHandler) handlePatchNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, err := decodePatchNotificationEndpointRequest(ctx, r)
	if err != nil {
		h.log.Debug("Failed to decode request", zap.Error(err))
		h.HandleHTTPError(ctx, err, w)
		return
	}

	edp, err := h.NotificationEndpointService.Update(ctx, endpoints.UpdateChangeSet(req.ID, req.Update))
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	labels, err := h.LabelService.FindResourceLabels(ctx, influxdb.LabelMappingFilter{
		ResourceID: edp.Base().ID,
	})
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}
	h.log.Debug("NotificationEndpoint patch", zap.String("notificationEndpoint", fmt.Sprint(edp)))

	if err := encodeResponse(ctx, w, http.StatusOK, newNotificationEndpointResponse(edp, labels)); err != nil {
		logEncodingError(h.log, r, err)
		return
	}
}

func (h *NotificationEndpointHandler) handleDeleteNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	i, err := decodeGetNotificationEndpointRequest(ctx)
	if err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	if err := h.NotificationEndpointService.Delete(ctx, i); err != nil {
		h.HandleHTTPError(ctx, err, w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// NotificationEndpointService is an http client for the influxdb.NotificationEndpointService server implementation.
type NotificationEndpointService struct {
	Client *httpc.Client
	*UserResourceMappingService
	*OrganizationService
}

// NewNotificationEndpointService constructs a new http NotificationEndpointService.
func NewNotificationEndpointService(client *httpc.Client) *NotificationEndpointService {
	return &NotificationEndpointService{
		Client: client,
		UserResourceMappingService: &UserResourceMappingService{
			Client: client,
		},
		OrganizationService: &OrganizationService{
			Client: client,
		},
	}
}

var _ influxdb.NotificationEndpointService = (*NotificationEndpointService)(nil)

// FindByID returns a single notification endpoint by ID.
func (s *NotificationEndpointService) FindByID(ctx context.Context, id influxdb.ID) (influxdb.NotificationEndpoint, error) {
	var resp notificationEndpointDecoder
	err := s.Client.
		Get(prefixNotificationEndpoints, id.String()).
		DecodeJSON(&resp).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	return resp.endpoint, nil
}

// Find returns a list of notification endpoints that match filter and the total count of matching notification endpoints.
// Additional options provide pagination & sorting.
func (s *NotificationEndpointService) Find(ctx context.Context, filter influxdb.NotificationEndpointFilter, opt ...influxdb.FindOptions) ([]influxdb.NotificationEndpoint, error) {
	params := findOptionParams(opt...)
	if filter.OrgID != nil {
		params = append(params, [2]string{"orgID", filter.OrgID.String()})
	}
	if filter.Org != nil {
		params = append(params, [2]string{"org", *filter.Org})
	}

	var resp struct {
		Endpoints []notificationEndpointDecoder `json:"notificationEndpoints"`
	}
	err := s.Client.
		Get(prefixNotificationEndpoints).
		QueryParams(params...).
		DecodeJSON(&resp).
		Do(ctx)
	if err != nil {
		return nil, err
	}

	var endpoints []influxdb.NotificationEndpoint
	for _, e := range resp.Endpoints {
		endpoints = append(endpoints, e.endpoint)
	}
	return endpoints, nil
}

// Create creates a new notification endpoint and sets b.ID with the new identifier.
// TODO(@jsteenb2): this is unsatisfactory, we have no way of grabbing the new notification endpoint without
//  serious hacky hackertoning. Put it on the list...
func (s *NotificationEndpointService) Create(ctx context.Context, _ influxdb.ID, ne influxdb.NotificationEndpoint) error {
	// userID is ignored here since server reads it off
	// the token/auth. its a nothing burger here
	var resp notificationEndpointDecoder
	err := s.Client.
		PostJSON(&notificationEndpointEncoder{ne: ne}, prefixNotificationEndpoints).
		DecodeJSON(&resp).
		Do(ctx)
	if err != nil {
		return err
	}
	base := ne.Base()
	base.ID = resp.endpoint.Base().ID
	base.OrgID = resp.endpoint.Base().OrgID
	return nil
}

// Update updates a single notification endpoint.
// Returns the new notification endpoint after update.
func (s *NotificationEndpointService) Update(ctx context.Context, update influxdb.EndpointUpdate) (influxdb.NotificationEndpoint, error) {
	switch update.UpdateType {
	case "endpoint":
		return s.update(ctx, update)
	case "change_set":
		return s.patch(ctx, update)
	default:
		return nil, &influxdb.Error{
			Code: influxdb.EInvalid,
			Msg:  "invalid update provided",
		}
	}

}

func (s *NotificationEndpointService) update(ctx context.Context, update influxdb.EndpointUpdate) (influxdb.NotificationEndpoint, error) {
	endpoint, _ := update.Fn(time.Time{}, &endpoint.HTTP{})

	var resp notificationEndpointDecoder
	err := s.Client.
		PutJSON(&notificationEndpointEncoder{ne: endpoint}, prefixNotificationEndpoints, endpoint.Base().ID.String()).
		DecodeJSON(&resp).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	return resp.endpoint, nil
}

// PatchNotificationEndpoint updates a single  notification endpoint with changeset.
// Returns the new notification endpoint state after update.
func (s *NotificationEndpointService) patch(ctx context.Context, update influxdb.EndpointUpdate) (influxdb.NotificationEndpoint, error) {
	endpoint, _ := update.Fn(time.Time{}, &endpoint.HTTP{})

	var body influxdb.NotificationEndpointUpdate

	base := endpoint.Base()
	if name := base.Name; name != "" {
		body.Name = &name
	}
	if desc := base.Description; true {
		body.Description = &desc
	}
	if status := base.Status; status.Valid() == nil {
		body.Status = &status
	}

	var resp notificationEndpointDecoder
	err := s.Client.
		PatchJSON(body, prefixNotificationEndpoints, endpoint.Base().ID.String()).
		DecodeJSON(&resp).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	return resp.endpoint, nil
}

// Delete removes a notification endpoint by ID, returns secret fields, orgID for further deletion.
// TODO: axe this delete design, makes little sense in how its currently being done. Right now, as an http client,
//  I am forced to know how the store handles this and then figure out what the server does in between me and that store,
//  then see what falls out :flushed... for now returning nothing for secrets, orgID, and only returning an error. This makes
//  the code/design smell super obvious imo
func (s *NotificationEndpointService) Delete(ctx context.Context, id influxdb.ID) error {
	err := s.Client.
		Delete(prefixNotificationEndpoints, id.String()).
		Do(ctx)
	return err
}

type notificationEndpointEncoder struct {
	ne influxdb.NotificationEndpoint
}

func (n *notificationEndpointEncoder) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(n.ne)
	if err != nil {
		return nil, err
	}

	ughhh := make(map[string]interface{})
	if err := json.Unmarshal(b, &ughhh); err != nil {
		return nil, err
	}
	n.ne.BackfillSecretKeys()

	// this makes me queezy and altogether sad
	fieldMap := map[string]string{
		"-password":    "password",
		"-routing-key": "routingKey",
		"-token":       "token",
		"-username":    "username",
	}
	for _, sec := range n.ne.SecretFields() {
		var v string
		if sec.Value != nil {
			v = *sec.Value
		}
		ughhh[fieldMap[sec.Key]] = v
	}
	return json.Marshal(ughhh)
}

type notificationEndpointDecoder struct {
	endpoint influxdb.NotificationEndpoint
}

func (n *notificationEndpointDecoder) UnmarshalJSON(b []byte) error {
	newEndpoint, err := endpoint.UnmarshalJSON(b)
	if err != nil {
		return err
	}
	n.endpoint = newEndpoint
	return nil
}
