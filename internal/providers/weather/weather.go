// Package weather fetches current conditions and a short forecast from
// Open-Meteo — free, no signup, no API key (plan §11).
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const defaultEndpoint = "https://api.open-meteo.com/v1/forecast"

type settings struct {
	Latitude  float64 `yaml:"latitude"`
	Longitude float64 `yaml:"longitude"`
	Location  string  `yaml:"location"`      // display label
	Days      int     `yaml:"forecast_days"` // default 4
}

type Provider struct {
	lat, lon float64
	location string
	days     int
	endpoint string
	http     *http.Client
}

// New is the core.Factory for "weather".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	if s.Latitude == 0 && s.Longitude == 0 {
		return nil, fmt.Errorf("weather widget %q: latitude and longitude are required", cfg.Title)
	}
	days := s.Days
	if days <= 0 {
		days = 4
	}
	loc := s.Location
	if loc == "" {
		loc = cfg.Title
	}
	return &Provider{
		lat:      s.Latitude,
		lon:      s.Longitude,
		location: loc,
		days:     days,
		endpoint: defaultEndpoint,
		http:     &http.Client{},
	}, nil
}

type apiResponse struct {
	Current struct {
		Temp      float64 `json:"temperature_2m"`
		FeelsLike float64 `json:"apparent_temperature"`
		Code      int     `json:"weather_code"`
	} `json:"current"`
	Daily struct {
		Time []string  `json:"time"`
		Code []int     `json:"weather_code"`
		Max  []float64 `json:"temperature_2m_max"`
		Min  []float64 `json:"temperature_2m_min"`
	} `json:"daily"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(p.lat, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(p.lon, 'f', 4, 64))
	q.Set("current", "temperature_2m,apparent_temperature,weather_code")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min")
	q.Set("timezone", "auto")
	q.Set("forecast_days", strconv.Itoa(p.days+1)) // +1 so we keep N days after today

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return core.Payload{}, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return core.Payload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return core.Payload{}, fmt.Errorf("open-meteo: http %d", resp.StatusCode)
	}

	var data apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return core.Payload{}, err
	}

	w := &core.Weather{
		Location:   p.location,
		Current:    data.Current.Temp,
		FeelsLike:  data.Current.FeelsLike,
		Code:       data.Current.Code,
		Condition:  core.WMOCondition(data.Current.Code),
		ObservedAt: time.Now(),
	}
	for i := range data.Daily.Time {
		d, _ := time.Parse("2006-01-02", data.Daily.Time[i])
		day := core.WeatherDay{
			Date:      d,
			High:      at(data.Daily.Max, i),
			Low:       at(data.Daily.Min, i),
			Code:      at(data.Daily.Code, i),
			Condition: core.WMOCondition(at(data.Daily.Code, i)),
		}
		if i == 0 {
			w.Today = day
		} else {
			w.Forecast = append(w.Forecast, day)
		}
	}

	return core.Payload{Weather: w}, nil
}

func at[T any](s []T, i int) T {
	if i < len(s) {
		return s[i]
	}
	var zero T
	return zero
}
