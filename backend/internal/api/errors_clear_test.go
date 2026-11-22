package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func errTestServer(logs *bytes.Buffer) *server {
	s := &server{}
	s.Log = slog.New(slog.NewTextHandler(logs, nil))
	s.MaxBodyBytes = 1 << 20
	return s
}

// An unexpected failure never shows its text: stable code, safe phrase, short ref; the detail is only in the log under that ref.
func TestFailInternalHidesDetailAndLogsRef(t *testing.T) {
	var logs bytes.Buffer
	s := errTestServer(&logs)
	rec := httptest.NewRecorder()
	secret := "pq: relation \"agents\" does not exist (SQLSTATE 42P01)"
	s.fail(rec, fmt.Errorf("load agents: %w", errors.New(secret)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "pq:") || strings.Contains(rec.Body.String(), "SQLSTATE") || strings.Contains(rec.Body.String(), "load agents") {
		t.Fatalf("500 leaked internal text: %s", rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "internal" || body["code"] != "internal" || body["message"] == "" || len(body["ref"]) != 8 {
		t.Fatalf("unexpected body: %v", body)
	}
	if !strings.Contains(logs.String(), body["ref"]) || !strings.Contains(logs.String(), "SQLSTATE 42P01") {
		t.Fatalf("log must carry the ref and the full detail: %s", logs.String())
	}
}

// A drive 500 goes through the same safe path.
func TestDriveFailUnexpectedIsSafe(t *testing.T) {
	var logs bytes.Buffer
	s := errTestServer(&logs)
	rec := httptest.NewRecorder()
	s.driveFail(rec, errors.New("disk /var/data exploded"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "exploded") || !strings.Contains(rec.Body.String(), `"ref"`) {
		t.Fatalf("drive 500 = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "exploded") {
		t.Fatal("detail must be logged")
	}
}

// A malformed JSON body is a generic 400 with code bad_request, never the decoder's wording.
func TestDecodeMalformedJSONIsGeneric(t *testing.T) {
	var logs bytes.Buffer
	s := errTestServer(&logs)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name": oops`))
	rec := httptest.NewRecorder()
	var dst struct{ Name string }
	err := s.decode(rec, req, &dst)
	if err == nil {
		t.Fatal("expected an error")
	}
	out := httptest.NewRecorder()
	s.fail(out, err)
	if out.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", out.Code)
	}
	body := out.Body.String()
	if strings.Contains(body, "invalid character") || strings.Contains(body, "oops") || strings.Contains(body, "looking for") {
		t.Fatalf("leaked decoder text: %s", body)
	}
	var m map[string]string
	_ = json.Unmarshal(out.Body.Bytes(), &m)
	if m["code"] != "bad_request" || m["message"] != "La solicitud no tiene un formato válido" {
		t.Fatalf("unexpected body: %v", m)
	}
}
