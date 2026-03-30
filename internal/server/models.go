package server

import (
	"encoding/json"
	"net/http"
	"time"
)

type modelsResponse struct {
	Object string      `json:"object"`
	Data   []modelInfo `json:"data"`
}

type modelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Only GET is allowed")
		return
	}

	var models []modelInfo
	for id := range s.cfg.Models {
		models = append(models, modelInfo{
			ID:      id,
			Object:  "model",
			Created: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC).Unix(),
			OwnedBy: "eachlabs",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(modelsResponse{
		Object: "list",
		Data:   models,
	})
}
