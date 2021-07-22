package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/common-nighthawk/go-figure"
	"github.com/dhawton/log4g"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	"github.com/vzau/flight-parse/database"
	"github.com/vzau/flight-parse/geo"
	dbTypes "github.com/vzau/types/database"
	"gorm.io/gorm"
)

var facilities map[string]geo.Polygon
var fac []Facility
var positions []string
var num_positions int
var log = log4g.Category("main")

func main() {
	intro := figure.NewFigure("ZAU Parser", "", false).Slicify()
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

	log.Info("Loading positions...")
	jsonfile, err = os.Open("positions.json")
	if err != nil {
		log.Fatal(fmt.Sprintf("Failed to open positions.json: %s\n", err.Error()))
	}

	data, err = ioutil.ReadAll(jsonfile)
	defer jsonfile.Close()

	if err := json.Unmarshal(data, &positions); err != nil {
		log.Fatal(fmt.Sprintf("Failed to unmarshal positions: %s\n", err.Error()))
	}
	num_positions = len(positions)
	log.Debug(fmt.Sprintf("Number of positions: %d, positions: %v", num_positions, positions))

	log.Info("Connecting to database and handling migrations")
	database.Connect(Getenv("DB_USERNAME", "root"), Getenv("DB_PASSWORD", "secret"), Getenv("DB_HOSTNAME", "localhost"), Getenv("DB_PORT", "3306"), Getenv("DB_DATABASE", "zau"))

	log.Info("Running first time...")
	GetData()

	log.Info("Creating cron job...")
	jobs := cron.New()
	jobs.AddFunc("@every 2m", GetData)

	jobs.Start()

	for {
		time.Sleep(time.Minute)
	}
}

func GetData() {
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

	ProcessFlights(vatsimData.Flights)
	ProcessControllers(vatsimData.Controllers)
}

func ProcessControllers(controllers []VATSIMController) {
	go database.DB.Where("updated_at < ?", time.Now().Add((time.Minute*5)*-1)).Delete(&dbTypes.OnlineControllers{})
	for i := 0; i < len(controllers); i++ {
		go func(controller VATSIMController) {
			for j := 0; j < num_positions; j++ {
				if strings.HasPrefix(controller.Callsign, positions[j]) {
					c := dbTypes.OnlineControllers{}
					if err := database.DB.Where("callsign = ?", controller.Callsign).First(&c).Error; err != nil {
						if !errors.Is(err, gorm.ErrRecordNotFound) {
							log.Error(fmt.Sprintf("Error looking up online controller %s, %s\n", controller.Callsign, err.Error()))
							break
						}
					}

					c.CID = controller.CID
					c.Name = controller.Name
					c.Callsign = controller.Callsign
					c.Facility = "ZAU"
					c.Frequency = controller.Frequency
					c.LogonTime = controller.LogonTime

					if err := database.DB.Save(&c).Error; err != nil {
						log.Error(fmt.Sprintf("Error saving online controller info %s to database: %s\n", controller.Callsign, err.Error()))
					}
					break
				}
			}
		}(controllers[i])
	}
}

func ProcessFlights(flights []VATSIMFlight) {
	go database.DB.Where("updated_at < ?", time.Now().Add((time.Minute*5)*-1)).Delete(&dbTypes.Flights{})

	for i := 0; i < len(flights); i++ {
		go func(id int) {
			flight := flights[id]
			f := dbTypes.Flights{}
			if err := database.DB.Where("callsign = ?", flight.Callsign).First(&f).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Error("Error looking up flight callsign " + flight.Callsign + ", " + err.Error())
					return
				}
			}

			f.Aircraft = flight.FlightPlan.Aircraft
			f.CID = flight.CID
			f.Callsign = flight.Callsign
			f.Name = flight.Name
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
