package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/icholy/digest"
)

var (
	bind     = flag.String("bind", ":8080", "The address to bind to")
	address  = flag.String("address", "", "The URI of the printer")
	username = flag.String("username", "maker", "The username for the printer")
	password = flag.String("password", os.Getenv("PRUSA_LINK_PASSWORD"), "The password for the printer")
	timeout  = flag.Duration("timeout", 15*time.Second, "The timeout for metrics requests to the printer")
)

type ProxyHandler struct {
	address string
	client  *http.Client
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uri, err := url.JoinPath(h.address, r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest(r.Method, uri, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send the request
	res, err := h.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send the response
	defer res.Body.Close()
	for k, v := range res.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

type PrusaCollector struct {
	address string
	client  *http.Client
}

func (c *PrusaCollector) Describe(ch chan<- *prometheus.Desc) {
}

func (c *PrusaCollector) Collect(ch chan<- prometheus.Metric) {
	wg := sync.WaitGroup{}
	for _, f := range []func(chan<- prometheus.Metric) error{
		c.collectInfo,
		c.collectStatus,
		c.collectJobInfo,
	} {
		wg.Add(1)
		go func(f func(chan<- prometheus.Metric) error) {
			defer wg.Done()
			if err := f(ch); err != nil {
				ch <- prometheus.NewInvalidMetric(prometheus.NewDesc("prusa_error", "An error occurred", nil, nil), err)
			}
		}(f)
	}
	wg.Wait()
}

type PrinterInfo struct {
	Hostname         string   `json:"hostname"`
	Serial           string   `json:"serial"`
	NozzleDiameter   *float64 `json:"nozzle_diameter"`
	MinExtrusionTemp *int     `json:"min_extrusion_temp"`
}

func (c *PrusaCollector) collectInfo(ch chan<- prometheus.Metric) error {
	uri, err := url.JoinPath(c.address, "/api/v1/info")
	if err != nil {
		panic(err)
	}

	res, err := c.client.Get(uri)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", res.StatusCode)
	}

	var info PrinterInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return err
	}

	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("prusa_printer_info", "The hostname of the printer", []string{"hostname", "serial"}, nil),
		prometheus.GaugeValue,
		1,
		info.Hostname,
		info.Serial,
	)

	if info.NozzleDiameter != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_nozzle_diameter_millimeters", "The diameter of the nozzle", nil, nil),
			prometheus.GaugeValue,
			*info.NozzleDiameter,
		)
	}

	if info.MinExtrusionTemp != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_min_extrusion_temperature_celsius", "The minimum extrusion temperature", nil, nil),
			prometheus.GaugeValue,
			float64(*info.MinExtrusionTemp),
		)
	}

	return nil
}

type Status struct {
	Printer PrinterStatus `json:"printer"`
}

type PrinterStatus struct {
	State        string   `json:"state"`
	TempNozzle   *float64 `json:"temp_nozzle"`
	TargetNozzle *float64 `json:"target_nozzle"`
	TempBed      *float64 `json:"temp_bed"`
	TargetBed    *float64 `json:"target_bed"`
	AxisX        *float64 `json:"axis_x"`
	AxisY        *float64 `json:"axis_y"`
	AxisZ        *float64 `json:"axis_z"`
	Flow         *int     `json:"flow"`
	Speed        *int     `json:"speed"`
	FanHotend    *int     `json:"fan_hotend"`
	FanPrint     *int     `json:"fan_print"`
}

func (c *PrusaCollector) collectStatus(ch chan<- prometheus.Metric) error {
	uri, err := url.JoinPath(c.address, "/api/v1/status")
	if err != nil {
		panic(err)
	}

	res, err := c.client.Get(uri)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", res.StatusCode)
	}

	var status Status
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		return err
	}

	printerStatus := status.Printer

	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("prusa_printer_state", "The current state of the printer", []string{"state"}, nil),
		prometheus.GaugeValue,
		1,
		strings.ToLower(printerStatus.State),
	)

	if printerStatus.TempNozzle != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_temperature_celsius", "The current temperature reading", []string{"sensor"}, nil),
			prometheus.GaugeValue,
			*printerStatus.TempNozzle,
			"nozzle",
		)
	}

	if printerStatus.TargetNozzle != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_target_temperature_celsius", "The target temperature", []string{"sensor"}, nil),
			prometheus.GaugeValue,
			*printerStatus.TargetNozzle,
			"nozzle",
		)
	}

	if printerStatus.TempBed != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_temperature_celsius", "The current temperature reading", []string{"sensor"}, nil),
			prometheus.GaugeValue,
			*printerStatus.TempBed,
			"bed",
		)
	}

	if printerStatus.TargetBed != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_target_temperature_celsius", "The target temperature", []string{"sensor"}, nil),
			prometheus.GaugeValue,
			*printerStatus.TargetBed,
			"bed",
		)
	}

	if printerStatus.AxisX != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_axis_position", "The current axis position", []string{"axis"}, nil),
			prometheus.GaugeValue,
			*printerStatus.AxisX,
			"x",
		)
	}

	if printerStatus.AxisY != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_axis_position", "The current axis position", []string{"axis"}, nil),
			prometheus.GaugeValue,
			*printerStatus.AxisY,
			"y",
		)
	}

	if printerStatus.AxisZ != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_axis_position", "The current axis position", []string{"axis"}, nil),
			prometheus.GaugeValue,
			*printerStatus.AxisZ,
			"z",
		)
	}

	if printerStatus.Flow != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_flow_percent", "The current flow percentage", nil, nil),
			prometheus.GaugeValue,
			float64(*printerStatus.Flow),
		)
	}

	if printerStatus.Speed != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_speed_percent", "The current speed percentage", nil, nil),
			prometheus.GaugeValue,
			float64(*printerStatus.Speed),
		)
	}

	if printerStatus.FanHotend != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_fan_speed_rpm", "The current fan RPM", []string{"fan"}, nil),
			prometheus.GaugeValue,
			float64(*printerStatus.FanHotend),
			"hotend",
		)
	}

	if printerStatus.FanPrint != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_fan_speed_rpm", "The current fan RPM", []string{"fan"}, nil),
			prometheus.GaugeValue,
			float64(*printerStatus.FanPrint),
			"print",
		)
	}

	return nil
}

type JobInfo struct {
	State         string   `json:"state"`
	Progress      *float64 `json:"progress"`
	TimeRemaining *int     `json:"time_remaining"`
	TimePrinting  *int     `json:"time_printing"`
}

func (c *PrusaCollector) collectJobInfo(ch chan<- prometheus.Metric) error {
	uri, err := url.JoinPath(c.address, "/api/v1/job")
	if err != nil {
		panic(err)
	}

	res, err := c.client.Get(uri)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNoContent {
		// No job is running
		return nil
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", res.StatusCode)
	}

	var jobInfo JobInfo
	if err := json.NewDecoder(res.Body).Decode(&jobInfo); err != nil {
		return err
	}

	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("prusa_job_state", "The current state of the job", []string{"state"}, nil),
		prometheus.GaugeValue,
		1,
		strings.ToLower(jobInfo.State),
	)

	if jobInfo.Progress != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_job_progress_percent", "The current job progress", nil, nil),
			prometheus.GaugeValue,
			*jobInfo.Progress,
		)
	}

	if jobInfo.TimeRemaining != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_job_time_remaining_seconds", "The time remaining for the job", nil, nil),
			prometheus.GaugeValue,
			float64(*jobInfo.TimeRemaining),
		)
	}

	if jobInfo.TimePrinting != nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("prusa_job_time_printing_seconds", "The time the job has been printing", nil, nil),
			prometheus.GaugeValue,
			float64(*jobInfo.TimePrinting),
		)
	}

	return nil
}

func main() {
	flag.Parse()

	if *address == "" {
		log.Fatal("address is required")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(&PrusaCollector{
		address: *address,
		client: &http.Client{
			Transport: &digest.Transport{
				Username: *username,
				Password: *password,
			},
			Timeout: *timeout,
		},
	})

	http.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))
	http.Handle("/", &ProxyHandler{
		client: &http.Client{
			Transport: &digest.Transport{
				Username: *username,
				Password: *password,
			},
		},
		address: *address,
	})
	log.Fatal(http.ListenAndServe(*bind, nil))
}
