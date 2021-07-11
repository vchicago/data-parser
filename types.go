package main

import (
	"time"

	"github.com/vchicago/flight-parse/geo"
)

type Facility struct {
	ID       string      `json:"id"`
	Boundary [][]float64 `json:"coords"`
	Polygon  geo.Polygon
}

type FacilityPosition struct {
	ID        string   `json:"id"`
	Positions []string `json:"positions"`
}

type VATSIMData struct {
	Controllers []VATSIMController `json:"controllers"`
	Flights     []VATSIMFlight     `json:"pilots"`
}

type VATSIMController struct {
	CID       int       `json:"cid"`
	Name      string    `json:"name"`
	Callsign  string    `json:"callsign"`
	Frequency string    `json:"frequency"`
	LogonTime time.Time `json:"logon_time"`
}

type VATSIMFlight struct {
	CID         int              `json:"cid"`
	Callsign    string           `json:"callsign"`
	Name        string           `json:"name"`
	Latitude    float64          `json:"latitude"`
	Longitude   float64          `json:"longitude"`
	Altitude    int              `json:"altitude"`
	Groundspeed int              `json:"groundspeed"`
	Heading     int              `json:"heading"`
	FlightPlan  VATSIMFlightPlan `json:"flight_plan"`
}

type VATSIMFlightPlan struct {
	Aircraft  string `json:"aircraft_faa"`
	Departure string `json:"departure"`
	Arrival   string `json:"arrival"`
	Route     string `json:"route"`
}
