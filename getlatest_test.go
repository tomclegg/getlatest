package main

import (
	"testing"
	"time"
)

func TestShouldAtTime(t *testing.T) {
	defaults := getter{}
	beforework := getter{
		NotBefore: "07:00",
		NotAfter:  "09:00",
		Weekdays:  "Mon Tue Wed Thu Fri",
	}
	for _, trial := range []struct {
		should bool
		t      string
		g      getter
	}{
		{false, "2019-08-28T04:00:00-07:00", beforework},
		{true, "2019-08-28T07:00:00-07:00", beforework},
		{true, "2019-08-28T08:59:00-07:00", beforework},
		{false, "2019-08-28T09:15:00-07:00", beforework},
		{false, "2019-08-31T08:59:00-07:00", beforework},
		{true, "2019-08-31T01:23:45-07:00", defaults},
		{false, "2019-08-10T01:23:45-07:00", getter{lastSuccess: time.Now()}},
	} {
		now, err := time.Parse(time.RFC3339, trial.t)
		if err != nil {
			t.Fatal(err)
		}
		g := trial.g
		g.URL = "http://host.example/foo"
		g.TTL = "1h"
		err = g.setup()
		if err != nil {
			t.Errorf("setup fail: %s", err)
			continue
		}
		if trial.should != g.should(now) {
			t.Errorf("fail: %#v", trial)
		}
	}
}
