package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

const sample = `{
  "current": {"temperature_2m": 15.3, "apparent_temperature": 14.3, "weather_code": 3},
  "daily": {
    "time": ["2026-09-08","2026-09-09","2026-09-10","2026-09-11","2026-09-12"],
    "weather_code": [55, 65, 3, 3, 0],
    "temperature_2m_max": [22.3, 23.5, 20.9, 19.4, 23.0],
    "temperature_2m_min": [14.5, 13.2, 13.2, 9.4, 12.1]
  }
}`

func widgetConfig(t *testing.T, src string) core.WidgetConfig {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	wc, err := core.ParseWidget(*doc.Content[0])
	if err != nil {
		t.Fatalf("ParseWidget: %v", err)
	}
	return wc
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("forecast_days") != "4" {
			t.Errorf("forecast_days = %q, want 4 (days + today)", r.URL.Query().Get("forecast_days"))
		}
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: weather\ntitle: Test City\nlatitude: 45.5\nlongitude: -73.57\nforecast_days: 3\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// point the provider at the test server
	p.(*Provider).endpoint = srv.URL

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	w := got.Weather
	if w == nil {
		t.Fatal("payload has no Weather")
	}
	if w.Location != "Test City" {
		t.Errorf("Location = %q", w.Location)
	}
	if w.Current != 15.3 || w.FeelsLike != 14.3 {
		t.Errorf("current = %v / feels %v", w.Current, w.FeelsLike)
	}
	if w.Condition != "Overcast" {
		t.Errorf("Condition = %q, want Overcast (code 3)", w.Condition)
	}
	if w.Today.High != 22.3 || w.Today.Low != 14.5 {
		t.Errorf("today = %v/%v", w.Today.High, w.Today.Low)
	}
	if len(w.Forecast) != 4 {
		t.Fatalf("forecast has %d days, want 4 (today excluded)", len(w.Forecast))
	}
	if w.Forecast[0].Condition != "Rain" { // code 65
		t.Errorf("forecast[0] condition = %q", w.Forecast[0].Condition)
	}
}

func TestRequiresCoordinates(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: weather\ntitle: Nowhere\n")); err == nil {
		t.Error("expected an error without latitude/longitude")
	}
}
