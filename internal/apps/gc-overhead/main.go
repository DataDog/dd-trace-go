// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// gc-overhead implements a http service that demonstrates high GC overhead. The
// primary use case is to take screenshots of CPU and Memory profiles for blog
// posts. The code is intentionally inefficient, but should produce plausible
// FlameGraphs. Loop and data sizes are chosen so that the hotspots in the CPU
// profile, the Allocated Memory Profile, and the Heap Live Objects profile are
// different.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/internal/apps/v2"
)

func main() {
	// Initialize fake data
	initFakeData()

	// Experimentally determined value to keep GC overhead around 30%.
	debug.SetGCPercent(35)

	// Start app
	app := apps.Config{}
	app.RunHTTP(func() http.Handler {
		mux := httptrace.NewServeMux()
		mux.HandleFunc("/vehicles/update_location", VehiclesUpdateLocationHandler)
		mux.HandleFunc("/vehicles/list", VehiclesListHandler)
		return mux
	})
}

func VehiclesUpdateLocationHandler(w http.ResponseWriter, r *http.Request) {
	load := int(sineLoad() * 2e5)
	for i := 0; i < load; i++ {
		u := &VehicleLocationUpdate{}
		data := fakeData.vehicleLocationUpdates[i%len(fakeData.vehicleLocationUpdates)]
		if err := parseVehicleLocationUpdate(data, u); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		store.Update(u)
	}
	w.Write([]byte("ok"))
}

func parseVehicleLocationUpdate(data []byte, u *VehicleLocationUpdate) error {
	return json.Unmarshal(data, u)
}

func VehiclesListHandler(w http.ResponseWriter, r *http.Request) {
	w.Write(renderVehiclesList().Bytes())
}

func renderVehiclesList() *bytes.Buffer {
	buf := &bytes.Buffer{}
	list := store.List()
	load := sineLoad() * float64(len(list))
	list = list[0:int(load)]
	for _, v := range list {
		fmt.Fprintf(buf, "%s: %v\n", v.ID, v.History)
	}
	return buf
}

var fakeData struct {
	vehicleLocationUpdates [1000][]byte
}

var store = MemoryStore{}

func initFakeData() {
	for i := 0; i < len(fakeData.vehicleLocationUpdates); i++ {
		update := VehicleLocationUpdate{
			ID: fmt.Sprintf("vehicle-%d", i),
			Position: Position{
				Longitude: rand.Float64()*180 - 90,
				Latitude:  rand.Float64()*360 - 180,
			},
		}
		fakeData.vehicleLocationUpdates[i], _ = json.Marshal(update)
	}
}

type MemoryStore struct {
	mu       sync.RWMutex
	vehicles map[string]*Vehicle
}

func (m *MemoryStore) Update(u *VehicleLocationUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vehicles == nil {
		m.vehicles = make(map[string]*Vehicle)
	}

	vehicle, ok := m.vehicles[u.ID]
	if !ok {
		vehicle = NewVehicle(u.ID)
		m.vehicles[u.ID] = vehicle
	}
	vehicle.History = append(vehicle.History, &u.Position)
	const historyLimit = 2000
	if len(vehicle.History) > historyLimit {
		// Keep only the last positions
		copy(vehicle.History, vehicle.History[len(vehicle.History)-historyLimit:])
		vehicle.History = vehicle.History[:historyLimit]
	}
}

func NewVehicle(id string) *Vehicle {
	return &Vehicle{ID: id, Data: make([]byte, 1024*1024)}
}

func (m *MemoryStore) List() (vehicles []*Vehicle) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, key := range slices.Sorted(maps.Keys(m.vehicles)) {
		vehicles = append(vehicles, m.vehicles[key].Copy())
	}
	return vehicles
}

type Position struct {
	Longitude float64
	Latitude  float64
}

type VehicleLocationUpdate struct {
	ID       string
	Position Position
}

type Vehicle struct {
	ID      string
	History []*Position
	Data    []byte
}

func (v *Vehicle) Copy() *Vehicle {
	history := make([]*Position, len(v.History))
	copy(history, v.History)
	return &Vehicle{
		ID:      v.ID,
		History: history,
	}
}

// sineLoad returns a value between 0 and 1 that varies sinusoidally over time.
func sineLoad() float64 {
	period := 5 * time.Minute
	// Get the current time in seconds since Unix epoch
	currentTime := time.Now().UnixNano()
	// Compute the phase of the sine wave, current time modulo period
	phase := float64(currentTime) / float64(period) * 2 * math.Pi
	// Generate the sine wave value (-1 to 1)
	sineValue := math.Sin(phase)
	// Normalize the sine wave value to be between 0 and 1
	return (sineValue + 1) * 0.5
}
