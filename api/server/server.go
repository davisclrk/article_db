package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/davisclrk/article_db/internal/coordinator"
	"github.com/davisclrk/article_db/internal/models"
)

type Server struct {
	coord *coordinator.Coordinator
	addr  string
}

func NewServer(coord *coordinator.Coordinator, addr string) *Server {
	return &Server{
		coord: coord,
		addr:  addr,
	}
}

type InsertRequest struct {
	URL      string    `json:"url"`
	Headline string    `json:"headline"`
	Summary  string    `json:"summary"`
	Content  string    `json:"content"`
	Vector   []float32 `json:"vector"`
}

type InsertResponse struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}

type QueryRequest struct {
	Vector []float32 `json:"vector"`
	K      int       `json:"k"`
}

type QueryResponse struct {
	Results []models.SearchResult `json:"results"`
	Error   string                `json:"error,omitempty"`
}

func (s *Server) Start() error {
	http.HandleFunc("/insert", s.handleInsert)
	http.HandleFunc("/get", s.handleGet)
	http.HandleFunc("/delete", s.handleDelete)
	http.HandleFunc("/query", s.handleQuery)
	http.HandleFunc("/health", s.handleHealth)

	fmt.Printf("Starting server on %s\n", s.addr)
	return http.ListenAndServe(s.addr, nil)
}

func (s *Server) handleInsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req InsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, InsertResponse{Error: err.Error()})
		return
	}

	article := &models.Article{
		URL:      req.URL,
		Headline: req.Headline,
		Summary:  req.Summary,
		Content:  req.Content,
		Vector:   req.Vector,
	}

	id, err := s.coord.Insert(article)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, InsertResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, InsertResponse{ID: id})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	article, err := s.coord.Get(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if article == nil {
		http.Error(w, "Article not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, article)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	if err := s.coord.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, QueryResponse{Error: err.Error()})
		return
	}

	if req.K <= 0 {
		req.K = 10
	}

	results, err := s.coord.Query(req.Vector, req.K)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, QueryResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, QueryResponse{Results: results})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
