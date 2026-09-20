package web

import "net/http"

// settingsVM is what settings.html renders. Spotify's card is filled in once
// the integration itself lands; for now every entry just reports its status.
type settingsVM struct {
	Integrations []integrationVM
}

type integrationVM struct {
	Name   string
	Status string
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	vm := settingsVM{
		Integrations: []integrationVM{
			{Name: "Spotify", Status: "not yet available"},
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "settings.html", vm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
