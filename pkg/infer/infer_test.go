package infer

import (
	"fmt"
	"reflect"
	"testing"

	"istio.io/api/networking/v1alpha3"
)

var ipWorkloadEntry = &v1alpha3.WorkloadEntry{Address: "8.8.8.8"}
var hostnameWorkloadEntry = &v1alpha3.WorkloadEntry{Address: "demo.tetrate.io"}

func TestResolution(t *testing.T) {
	tests := []struct {
		name            string
		workloadEntries []*v1alpha3.WorkloadEntry
		want            v1alpha3.ServiceEntry_Resolution
	}{
		{
			name:            "hostname workload entries infer DNS",
			workloadEntries: []*v1alpha3.WorkloadEntry{hostnameWorkloadEntry},
			want:            v1alpha3.ServiceEntry_DNS,
		},
		{
			name:            "IP only workload entries infer STATIC",
			workloadEntries: []*v1alpha3.WorkloadEntry{ipWorkloadEntry},
			want:            v1alpha3.ServiceEntry_STATIC,
		},
		{
			name:            "Mixed workload entries infer DNS",
			workloadEntries: []*v1alpha3.WorkloadEntry{ipWorkloadEntry, hostnameWorkloadEntry},
			want:            v1alpha3.ServiceEntry_DNS,
		},
		{
			name:            "nil workload entries infer DNS",
			workloadEntries: nil,
			want:            v1alpha3.ServiceEntry_DNS,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolution(tt.workloadEntries); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Resolution() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPorts(t *testing.T) {
	tests := []struct {
		name            string
		workloadEntries []*v1alpha3.WorkloadEntry
		want            []*v1alpha3.ServicePort
	}{
		{
			name: "Two workload entries with different ports creates two ports",
			workloadEntries: []*v1alpha3.WorkloadEntry{
				{Address: "1.1.1.1", Ports: map[string]uint32{"http": 80}},
				{Address: "8.8.8.8", Ports: map[string]uint32{"https": 443}},
			},
			want: []*v1alpha3.ServicePort{
				{Number: 80, Name: "http", Protocol: "HTTP"},
				{Number: 443, Name: "https", Protocol: "HTTPS"},
			},
		},
		{
			name: "Two workload entries with the same port are de-duped",
			workloadEntries: []*v1alpha3.WorkloadEntry{
				{Address: "1.1.1.1", Ports: map[string]uint32{"http": 80}},
				{Address: "8.8.8.8", Ports: map[string]uint32{"http": 80}},
			},
			want: []*v1alpha3.ServicePort{{Number: 80, Name: "http", Protocol: "HTTP"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Ports(tt.workloadEntries); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Ports() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkloadEntry(t *testing.T) {
	tests := []struct {
		name    string
		address string
		port    uint32
		want    *v1alpha3.WorkloadEntry
	}{
		{
			name:    "Generates a Workload Entry from an address port pair",
			address: "1.1.1.1",
			port:    80,
			want: &v1alpha3.WorkloadEntry{
				Address: "1.1.1.1",
				Ports:   map[string]uint32{"http": 80},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkloadEntry(tt.address, tt.port); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("WorkloadEntry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProto(t *testing.T) {
	tests := []struct {
		port uint32
		want string
	}{
		{port: 80, want: "http"},
		{port: 443, want: "https"},
		{port: 1234, want: "tcp"},
		{port: 4321, want: "tcp"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v is %v", tt.port, tt.want), func(t *testing.T) {
			if got := Proto(tt.port); got != tt.want {
				t.Errorf("Proto() = %v, want %v", got, tt.want)
			}
		})
	}
}
