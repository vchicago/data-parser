package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/common-nighthawk/go-figure"
	"github.com/dhawton/log4g"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	"github.com/vchicago/flight-parse/database"
	"github.com/vchicago/flight-parse/geo"
	kzdvTypes "github.com/vchicago/types/database"
	"gorm.io/gorm"
)

var facilities map[string]geo.Polygon
var fac []Facility
var log = log4g.Category("main")

func main() {
	intro := figure.NewFigure("ZAU FP", "", false).Slicify()
	for i := 0; i < len(intro); i++ {
		log.Info(intro[i])
	}

	log.Info("Checking for .env, loading if exists")
	if _, err := os.Stat(".env"); err == nil {
		log.Info("Found, loading")
		err := godotenv.Load()
		if err != nil {
			log.Error("Error loading .env file: " + err.Error())
		}
	}
	log4g.SetLogLevel(log4g.DEBUG)

	log.Info("Loading facility boundaries...")
	jsonfile, err := os.Open("boundaries.json")
	if err != nil {
		log.Fatal(fmt.Sprintf("Failed to open boundaries.json: %s\n", err.Error()))
	}

	data, err := ioutil.ReadAll(jsonfile)
	defer jsonfile.Close()

	if err := json.Unmarshal(data, &fac); err != nil {
		log.Fatal(fmt.Sprintf("Failed to unmarshal facility boundaries: %s\n", err.Error()))
	}

	log.Info("Generating Polygons...")
	for i := 0; i < len(fac); i++ {
		var points []geo.Point
		for j := 0; j < len(fac[i].Boundary); j++ {
			points = append(points, geo.Point{X: fac[i].Boundary[j][0], Y: fac[i].Boundary[j][1]})
		}
		fac[i].Polygon = geo.Polygon{Points: points}
	}

	log.Info("Connecting to database and handling migrations")
	database.Connect(Getenv("DB_USERNAME", "root"), Getenv("DB_PASSWORD", "secret"), Getenv("DB_HOSTNAME", "localhost"), Getenv("DB_PORT", "3306"), Getenv("DB_DATABASE", "zau"))

	log.Info("Running first time...")
	ProcessFlights()

	log.Info("Creating cron job...")
	jobs := cron.New()
	jobs.AddFunc("@every 2m", ProcessFlights)

	jobs.Start()

	for {
		time.Sleep(time.Minute)
	}
}

func ProcessFlights() {
	url := "https://data.vatsim.net/v3/vatsim-data.json"
	resp, err := http.Get(url)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to get stats from VATSIM: %s" + err.Error()))
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		log.Error("Failed to read body from VATSIM: " + err.Error())
		return
	}

	vatsimData := VATSIMData{}
	if err := json.Unmarshal(body, &vatsimData); err != nil {
		log.Error(fmt.Sprintf("Failed to unmarshal data from VATSIM: %s", err.Error()))
		return
	}

	log.Debug(fmt.Sprintf("Processing %d flights", len(vatsimData.Flights)))

	// Delete old flights
	go database.DB.Where("updated_at < ?", time.Now().Add((time.Minute*5)*-1)).Delete(&kzdvTypes.Flights{})

	for i := 0; i < len(vatsimData.Flights); i++ {
		go func(id int) {
			flight := vatsimData.Flights[id]
			f := kzdvTypes.Flights{}
			if err := database.DB.Where("callsign = ?", flight.Callsign).First(&f).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Error("Error looking up flight callsign " + flight.Callsign + ", " + err.Error())
					return
				}
			}

			f.Aircraft = flight.FlightPlan.Aircraft
			f.CID = flight.CID
			f.Callsign = flight.Callsign
			f.Latitude = float32(flight.Latitude)
			f.Longitude = float32(flight.Longitude)
			f.Altitude = flight.Altitude
			f.Heading = flight.Heading
			f.Groundspeed = flight.Groundspeed
			f.Departure = flight.FlightPlan.Departure
			f.Arrival = flight.FlightPlan.Arrival
			f.Route = flight.FlightPlan.Route
			f.Facility = ""

			if f.Latitude < 75.0 && f.Latitude > 21.0 && f.Longitude < -50.0 && f.Longitude > -179.0 {
				for j := 0; j < len(fac); j++ {
					facId := fac[j].ID
					poly := fac[j].Polygon
					p := geo.Point{X: float64(f.Longitude), Y: float64(f.Latitude)}
					if geo.PointInPolygon(p, poly) {
						f.Facility = facId
					}
				}
			}

			if err := database.DB.Save(&f).Error; err != nil {
				log.Error("Error saving flight information for " + f.Callsign + " to database: " + err.Error())
			}
		}(i)
	}
}
