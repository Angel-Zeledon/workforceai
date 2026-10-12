package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

// mountLLM registers the bring-your-own-model endpoints (G3). Owners and admins only (org:manage), also
// for reading. No response ever contains an API key: only its last 4 characters. No-op when Deps.LLM is nil.
func (s *server) mountLLM(r chi.Router) {
	if s.LLM == nil {
		return
	}
	manage := s.can(auth.PermOrgManage)
	r.With(manage).Get("/settings/llm", s.getLLM)
	r.With(manage).Put("/settings/llm/providers/{provider}", s.putLLMProvider)
	r.With(manage).Delete("/settings/llm/providers/{provider}", s.deleteLLMProvider)
	r.With(manage).Post("/settings/llm/providers/{provider}/test", s.testLLMProvider)
	r.With(manage).Put("/settings/llm/active", s.putLLMActive)
}

func (s *server) llmFail(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrLLMEncryptionOff) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error(), "code": "encryption_unavailable"})
		return
	}
	s.fail(w, err)
}

func (s *server) getLLM(w http.ResponseWriter, r *http.Request) {
	v, err := s.LLM.Get(r.Context())
	if err != nil {
		s.llmFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putLLMProvider(w http.ResponseWriter, r *http.Request) {
	var in application.LLMInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.LLM.Set(r.Context(), chi.URLParam(r, "provider"), in)
	if err != nil {
		s.llmFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) deleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	v, err := s.LLM.Delete(r.Context(), chi.URLParam(r, "provider"))
	if err != nil {
		s.llmFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) testLLMProvider(w http.ResponseWriter, r *http.Request) {
	var in application.LLMInput
	if r.ContentLength != 0 {
		if err := s.decode(w, r, &in); err != nil {
			s.fail(w, err)
			return
		}
	}
	res, err := s.LLM.Test(r.Context(), chi.URLParam(r, "provider"), in)
	if err != nil {
		s.llmFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) putLLMActive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.LLM.SetActive(r.Context(), in.Provider)
	if err != nil {
		s.llmFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
