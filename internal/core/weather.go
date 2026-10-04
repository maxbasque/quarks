package core

import "time"

// Weather is the payload for a weather widget. It deliberately does not reuse
// Item — forcing it into that shape would help nothing (plan §6).
type Weather struct {
	Location   string       `json:"location"`
	Current    float64      `json:"current"`     // °C
	FeelsLike  float64      `json:"feels_like"`  // °C
	Code       int          `json:"code"`        // WMO weather-interpretation code
	Condition  string       `json:"condition"`   // human label for Code
	Today      WeatherDay   `json:"today"`       //
	Forecast   []WeatherDay `json:"forecast"`    // upcoming days, today excluded
	ObservedAt time.Time    `json:"observed_at"` //
}

type WeatherDay struct {
	Date      time.Time `json:"date"`
	High      float64   `json:"high"` // °C
	Low       float64   `json:"low"`  // °C
	Code      int       `json:"code"`
	Condition string    `json:"condition"`
}

// WMOCondition maps a WMO weather-interpretation code to a short label.
// https://open-meteo.com/en/docs — "Weather variable documentation".
func WMOCondition(code int) string {
	switch code {
	case 0:
		return "Dégagé"
	case 1:
		return "Généralement dégagé"
	case 2:
		return "Partiellement nuageux"
	case 3:
		return "Couvert"
	case 45, 48:
		return "Brouillard"
	case 51, 53, 55:
		return "Bruine"
	case 56, 57:
		return "Bruine verglaçante"
	case 61, 63, 65:
		return "Pluie"
	case 66, 67:
		return "Pluie verglaçante"
	case 71, 73, 75:
		return "Neige"
	case 77:
		return "Neige en grains"
	case 80, 81, 82:
		return "Averses de pluie"
	case 85, 86:
		return "Averses de neige"
	case 95:
		return "Orages"
	case 96, 99:
		return "Orages avec grêle"
	default:
		return "—"
	}
}
