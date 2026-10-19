package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/auth"
	"github.com/go-chi/chi/v5"
)

const (
	schemaUser  = "urn:ietf:params:scim:schemas:core:2.0:User"
	schemaGroup = "urn:ietf:params:scim:schemas:core:2.0:Group"
	schemaList  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	schemaError = "urn:ietf:params:scim:api:messages:2.0:Error"
	schemaPatch = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	schemaSPC   = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	contentType = "application/scim+json"
	maxBody     = 1 << 20
	maxPageSize = 100
)

// HandlerConfig configures the HTTP layer.
type HandlerConfig struct {
	Limiter        auth.Limiter
	TrustedProxies []netip.Prefix
	FailOpen       bool
	// BaseURL is the public URL of the SCIM root (https://host/api/v1/scim/v2), used for meta.location.
	BaseURL string
}

// Handler serves /Users and /Groups for the organization that owns the bearer token.
type Handler struct {
	svc *Service
	cfg HandlerConfig
}

// NewHandler builds the HTTP layer.
func NewHandler(svc *Service, cfg HandlerConfig) *Handler { return &Handler{svc: svc, cfg: cfg} }

type orgKey struct{}
type actorKey struct{}

// Routes is the SCIM root (mounted at /api/v1/scim/v2).
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	if h.cfg.Limiter != nil {
		r.Use(auth.RateLimit(h.cfg.Limiter, auth.Limit{Max: 1200, Window: time.Minute}, auth.IPKey("scim", h.cfg.TrustedProxies), h.cfg.FailOpen))
	}
	r.Use(h.bearer)
	r.Get("/ServiceProviderConfig", h.serviceProviderConfig)
	r.Get("/ResourceTypes", h.resourceTypes)
	r.Get("/Schemas", h.schemas)
	r.Get("/Users", h.listUsers)
	r.Post("/Users", h.createUser)
	r.Get("/Users/{id}", h.getUser)
	r.Put("/Users/{id}", h.putUser)
	r.Patch("/Users/{id}", h.patchUser)
	r.Delete("/Users/{id}", h.deleteUser)
	r.Get("/Groups", h.listGroups)
	r.Post("/Groups", h.createGroup)
	r.Get("/Groups/{id}", h.getGroup)
	r.Put("/Groups/{id}", h.putGroup)
	r.Patch("/Groups/{id}", h.patchGroup)
	r.Delete("/Groups/{id}", h.deleteGroup)
	return r
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var se *Error
	if !errors.As(err, &se) {
		se = &Error{Status: 500, Message: "internal error"}
	}
	b := map[string]any{"schemas": []string{schemaError}, "status": strconv.Itoa(se.Status), "detail": se.Message}
	if se.Type != "" {
		b["scimType"] = se.Type
	}
	write(w, se.Status, b)
}

func (h *Handler) bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if len(a) < 8 || !strings.EqualFold(a[:7], "bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="scim"`)
			writeErr(w, errf(401, "", "missing bearer token"))
			return
		}
		org, err := h.svc.Authenticate(r.Context(), strings.TrimSpace(a[7:]))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="scim"`)
			writeErr(w, errf(401, "", "invalid token"))
			return
		}
		ctx := context.WithValue(r.Context(), orgKey{}, org)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func orgOf(r *http.Request) string { o, _ := r.Context().Value(orgKey{}).(string); return o }

const actor = "scim"

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, errf(400, "invalidSyntax", "invalid JSON body"))
		return false
	}
	return true
}

// ---- discovery documents ----

func (h *Handler) serviceProviderConfig(w http.ResponseWriter, _ *http.Request) {
	write(w, 200, map[string]any{
		"schemas":        []string{schemaSPC},
		"patch":          map[string]bool{"supported": true},
		"bulk":           map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":         map[string]any{"supported": true, "maxResults": maxPageSize},
		"changePassword": map[string]bool{"supported": false},
		"sort":           map[string]bool{"supported": false},
		"etag":           map[string]bool{"supported": false},
		"authenticationSchemes": []map[string]any{{"type": "oauthbearertoken", "name": "Bearer token",
			"description": "One long-lived bearer token per organization (Settings, Security)."}},
	})
}

func (h *Handler) resourceTypes(w http.ResponseWriter, _ *http.Request) {
	write(w, 200, listResponse([]any{
		map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": "User", "name": "User", "endpoint": "/Users", "schema": schemaUser},
		map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": "Group", "name": "Group", "endpoint": "/Groups", "schema": schemaGroup},
	}, 1))
}

func (h *Handler) schemas(w http.ResponseWriter, _ *http.Request) {
	write(w, 200, listResponse([]any{
		map[string]any{"id": schemaUser, "name": "User", "description": "User account (userName is the e-mail address)"},
		map[string]any{"id": schemaGroup, "name": "Group", "description": "Fixed role groups: admin, member, viewer"},
	}, 1))
}

func listResponse(res []any, start int) map[string]any {
	if res == nil {
		res = []any{}
	}
	return map[string]any{"schemas": []string{schemaList}, "totalResults": len(res), "startIndex": start, "itemsPerPage": len(res), "Resources": res}
}

// ---- users ----

func (h *Handler) userJSON(v View) map[string]any {
	return map[string]any{
		"schemas": []string{schemaUser}, "id": v.UserID, "externalId": v.ExternalID, "userName": v.UserName, "displayName": v.Name,
		"name":   map[string]string{"formatted": v.Name},
		"emails": []map[string]any{{"value": v.UserName, "type": "work", "primary": true}},
		"active": v.Active,
		"roles":  []map[string]any{{"value": string(v.Role), "primary": true}},
		"meta": map[string]any{"resourceType": "User", "created": v.CreatedAt.UTC().Format(time.RFC3339), "lastModified": v.UpdatedAt.UTC().Format(time.RFC3339),
			"location": strings.TrimRight(h.cfg.BaseURL, "/") + "/Users/" + v.UserID},
	}
}

type userBody struct {
	UserName    string          `json:"userName"`
	ExternalID  string          `json:"externalId"`
	DisplayName string          `json:"displayName"`
	Active      json.RawMessage `json:"active"`
	Name        struct {
		Formatted  string `json:"formatted"`
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	} `json:"name"`
	Emails []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
	} `json:"emails"`
	Roles []struct {
		Value string `json:"value"`
	} `json:"roles"`
}

// parseBool reads a SCIM boolean, tolerating the strings "True"/"False" that Microsoft Entra ID sends.
func parseBool(raw json.RawMessage) *bool {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return &b
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v := strings.EqualFold(strings.TrimSpace(s), "true")
		return &v
	}
	return nil
}

func (b userBody) input() UserInput {
	in := UserInput{UserName: b.UserName, ExternalID: b.ExternalID, DisplayName: b.DisplayName, Active: parseBool(b.Active)}
	if strings.TrimSpace(in.DisplayName) == "" {
		in.DisplayName = strings.TrimSpace(b.Name.Formatted)
		if in.DisplayName == "" {
			in.DisplayName = strings.TrimSpace(b.Name.GivenName + " " + b.Name.FamilyName)
		}
	}
	if in.UserName == "" { // some providers send only emails
		for _, e := range b.Emails {
			if e.Primary || in.UserName == "" {
				in.UserName = e.Value
			}
		}
	}
	for _, r := range b.Roles {
		if v := auth.Role(strings.ToLower(strings.TrimSpace(r.Value))); validRole(v) {
			in.Role = v
			break
		}
	}
	return in
}

var filterRE = regexp.MustCompile(`(?i)^\s*userName\s+eq\s+"([^"]{1,254})"\s*$`)

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	name := ""
	if f := r.URL.Query().Get("filter"); f != "" {
		m := filterRE.FindStringSubmatch(f)
		if m == nil {
			writeErr(w, errf(400, "invalidFilter", `only: userName eq "value"`))
			return
		}
		name = m[1]
	}
	all, err := h.svc.List(r.Context(), orgOf(r), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
	if start < 1 {
		start = 1
	}
	count := maxPageSize
	if c, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && c >= 0 && c < maxPageSize {
		count = c
	}
	var page []any
	for i := start - 1; i < len(all) && len(page) < count; i++ {
		page = append(page, h.userJSON(all[i]))
	}
	resp := listResponse(page, start)
	resp["totalResults"] = len(all)
	write(w, 200, resp)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var b userBody
	if !decode(w, r, &b) {
		return
	}
	v, err := h.svc.Create(r.Context(), orgOf(r), actor, b.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Location", strings.TrimRight(h.cfg.BaseURL, "/")+"/Users/"+v.UserID)
	write(w, 201, h.userJSON(v))
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Get(r.Context(), orgOf(r), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	write(w, 200, h.userJSON(v))
}

func (h *Handler) putUser(w http.ResponseWriter, r *http.Request) {
	var b userBody
	if !decode(w, r, &b) {
		return
	}
	v, err := h.svc.Replace(r.Context(), orgOf(r), actor, chi.URLParam(r, "id"), b.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	write(w, 200, h.userJSON(v))
}

type patchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type patchBody struct {
	Schemas    []string  `json:"schemas"`
	Operations []patchOp `json:"Operations"`
}

func (h *Handler) patchUser(w http.ResponseWriter, r *http.Request) {
	var b patchBody
	if !decode(w, r, &b) {
		return
	}
	org, id := orgOf(r), chi.URLParam(r, "id")
	if _, err := h.svc.Get(r.Context(), org, id); err != nil {
		writeErr(w, err)
		return
	}
	var active *bool
	var role auth.Role
	for _, op := range b.Operations {
		verb := strings.ToLower(op.Op)
		if verb != "add" && verb != "replace" && verb != "remove" {
			writeErr(w, errf(400, "invalidSyntax", "unknown op"))
			return
		}
		path := strings.ToLower(strings.TrimSpace(op.Path))
		switch {
		case path == "active":
			active = parseBool(op.Value)
		case path == "":
			var obj map[string]json.RawMessage // {"active": false, ...} (Okta)
			if json.Unmarshal(op.Value, &obj) == nil {
				for k, v := range obj {
					if strings.EqualFold(k, "active") {
						active = parseBool(v)
					}
				}
			}
		case strings.HasPrefix(path, "roles"):
			if verb != "remove" {
				role = roleFromValue(op.Value)
			}
		}
		// Every other attribute (names, phone numbers, ...) is accepted and ignored: the account's profile belongs to the person.
	}
	if role != "" {
		if _, err := h.svc.SetRole(r.Context(), org, actor, id, role); err != nil {
			writeErr(w, err)
			return
		}
	}
	if active != nil {
		if _, err := h.svc.SetActive(r.Context(), org, actor, id, *active); err != nil {
			writeErr(w, err)
			return
		}
	}
	v, err := h.svc.Get(r.Context(), org, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	write(w, 200, h.userJSON(v))
}

func roleFromValue(raw json.RawMessage) auth.Role {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return auth.Role(strings.ToLower(one))
	}
	var arr []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return auth.Role(strings.ToLower(arr[0].Value))
	}
	return ""
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if _, err := h.svc.SetActive(r.Context(), orgOf(r), actor, chi.URLParam(r, "id"), false); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- groups ----

func (h *Handler) groupJSON(g Group) map[string]any {
	members := make([]map[string]string, 0, len(g.Members))
	for _, m := range g.Members {
		members = append(members, map[string]string{"value": m.UserID, "display": m.UserName})
	}
	return map[string]any{"schemas": []string{schemaGroup}, "id": string(g.Role), "displayName": string(g.Role), "members": members,
		"meta": map[string]any{"resourceType": "Group", "location": strings.TrimRight(h.cfg.BaseURL, "/") + "/Groups/" + string(g.Role)}}
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := h.svc.Groups(r.Context(), orgOf(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	var out []any
	for _, g := range gs {
		out = append(out, h.groupJSON(g))
	}
	write(w, 200, listResponse(out, 1))
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.svc.GroupByID(r.Context(), orgOf(r), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	write(w, 200, h.groupJSON(g))
}

type groupBody struct {
	DisplayName string `json:"displayName"`
	Members     []struct {
		Value string `json:"value"`
	} `json:"members"`
}

func (b groupBody) ids() []string {
	var ids []string
	for _, m := range b.Members {
		ids = append(ids, m.Value)
	}
	return ids
}

func roleOfGroup(name string) (auth.Role, error) {
	r := auth.Role(strings.ToLower(strings.TrimSpace(name)))
	if !validRole(r) {
		return "", errf(400, "invalidValue", "groups are the roles admin, member and viewer")
	}
	return r, nil
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var b groupBody
	if !decode(w, r, &b) {
		return
	}
	role, err := roleOfGroup(b.DisplayName)
	if err == nil {
		err = h.svc.AddToGroup(r.Context(), orgOf(r), actor, role, b.ids())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	g, err := h.svc.GroupByID(r.Context(), orgOf(r), string(role))
	if err != nil {
		writeErr(w, err)
		return
	}
	write(w, 201, h.groupJSON(g))
}

func (h *Handler) putGroup(w http.ResponseWriter, r *http.Request) {
	var b groupBody
	if !decode(w, r, &b) {
		return
	}
	role, err := roleOfGroup(chi.URLParam(r, "id"))
	if err == nil {
		err = h.svc.ReplaceGroupMembers(r.Context(), orgOf(r), actor, role, b.ids())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	g, _ := h.svc.GroupByID(r.Context(), orgOf(r), string(role))
	write(w, 200, h.groupJSON(g))
}

var memberPathRE = regexp.MustCompile(`(?i)^members\[value\s+eq\s+"([^"]+)"\]$`)

func (h *Handler) patchGroup(w http.ResponseWriter, r *http.Request) {
	var b patchBody
	if !decode(w, r, &b) {
		return
	}
	org := orgOf(r)
	role, err := roleOfGroup(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, op := range b.Operations {
		verb, path := strings.ToLower(op.Op), strings.TrimSpace(op.Path)
		var vals []struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(op.Value, &vals)
		ids := make([]string, 0, len(vals))
		for _, v := range vals {
			ids = append(ids, v.Value)
		}
		switch {
		case strings.EqualFold(path, "members") && verb == "add":
			err = h.svc.AddToGroup(r.Context(), org, actor, role, ids)
		case strings.EqualFold(path, "members") && verb == "replace":
			err = h.svc.ReplaceGroupMembers(r.Context(), org, actor, role, ids)
		case strings.EqualFold(path, "members") && verb == "remove":
			if len(ids) == 0 { // "remove all members"
				var g Group
				if g, err = h.svc.GroupByID(r.Context(), org, string(role)); err == nil {
					for _, m := range g.Members {
						ids = append(ids, m.UserID)
					}
				}
			}
			if err == nil {
				err = h.svc.RemoveFromGroup(r.Context(), org, actor, role, ids)
			}
		case memberPathRE.MatchString(path) && verb == "remove":
			err = h.svc.RemoveFromGroup(r.Context(), org, actor, role, []string{memberPathRE.FindStringSubmatch(path)[1]})
		case path == "" && verb != "remove": // {"members":[...]} without a path
			var obj struct {
				Members []struct {
					Value string `json:"value"`
				} `json:"members"`
			}
			if json.Unmarshal(op.Value, &obj) == nil {
				for _, m := range obj.Members {
					ids = append(ids, m.Value)
				}
				err = h.svc.AddToGroup(r.Context(), org, actor, role, ids)
			}
		}
		if err != nil {
			writeErr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, _ *http.Request) { writeErr(w, errMutability) }

// ---- admin API for the Settings screen (authenticated as a person, not as the IdP) ----

// AdminRoutes manages the token: GET /scim, POST /scim/token (rotate; returns the token once), DELETE /scim/token.
// Mount it on a router that already requires an authenticated admin.
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/scim", func(w http.ResponseWriter, req *http.Request) {
		p, _ := auth.PrincipalFrom(req.Context())
		ti, err := h.svc.TokenInfo(req.Context(), p.OrgID)
		if err != nil {
			adminJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "internal error"}})
			return
		}
		adminJSON(w, 200, map[string]any{"token": ti, "base_url": h.cfg.BaseURL})
	})
	r.Post("/scim/token", func(w http.ResponseWriter, req *http.Request) {
		p, _ := auth.PrincipalFrom(req.Context())
		tok, err := h.svc.RotateToken(req.Context(), p.OrgID, p.UserID)
		if err != nil {
			adminJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "internal error"}})
			return
		}
		adminJSON(w, 200, map[string]any{"token": tok, "base_url": h.cfg.BaseURL}) // shown once
	})
	r.Delete("/scim/token", func(w http.ResponseWriter, req *http.Request) {
		p, _ := auth.PrincipalFrom(req.Context())
		if err := h.svc.RevokeToken(req.Context(), p.OrgID, p.UserID); err != nil {
			adminJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "internal error"}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func adminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
