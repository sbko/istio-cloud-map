package cloudmap

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdTypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
	"istio.io/api/networking/v1alpha3"

	"github.com/tetratelabs/istio-registry-sync/pkg/provider"
)

type mockSDAPI struct {
	ServiceDiscoveryClient

	ListNsResult   *servicediscovery.ListNamespacesOutput
	ListNsErr      error
	ListSvcResult  *servicediscovery.ListServicesOutput
	ListSvcErr     error
	DiscInstResult *servicediscovery.DiscoverInstancesOutput
	DiscInstErr    error
}

func (m *mockSDAPI) ListNamespaces(ctx context.Context, lni *servicediscovery.ListNamespacesInput, optFns ...func(*servicediscovery.Options)) (
	*servicediscovery.ListNamespacesOutput, error) {
	return m.ListNsResult, m.ListNsErr
}
func (m *mockSDAPI) ListServices(ctx context.Context, lsi *servicediscovery.ListServicesInput, optFns ...func(*servicediscovery.Options)) (
	*servicediscovery.ListServicesOutput, error) {
	filter := lsi.Filters[0]
	if filter.Condition != filterConditionEquals || filter.Name != serviceFilterNamespaceID {
		return nil, errors.New("Namespace ID filter is not present")
	}
	return m.ListSvcResult, m.ListSvcErr
}

func (m *mockSDAPI) DiscoverInstances(ctx context.Context, dii *servicediscovery.DiscoverInstancesInput, optFns ...func(*servicediscovery.Options)) (
	*servicediscovery.DiscoverInstancesOutput, error) {
	if dii.ServiceName == nil {
		return nil, errors.New("Service name was not provided")
	}
	if dii.NamespaceName == nil {
		return nil, errors.New("Namespace name was not provided")
	}
	return m.DiscInstResult, m.DiscInstErr
}

// various strings to allow pointer usage
var ipv41, ipv42, subdomain, hostname, portStr, httpPortStr = "8.8.8.8", "9.9.9.9", "demo", "tetrate.io", "9999", "80"
var cname = fmt.Sprintf("%v.%v", subdomain, hostname)

// golden path responses
var inferedIPv41WorkloadEntry = &v1alpha3.WorkloadEntry{Address: ipv41, Ports: map[string]uint32{"http": 80, "https": 443}}
var inferedIPv42WorkloadEntry = &v1alpha3.WorkloadEntry{Address: ipv42, Ports: map[string]uint32{"http": 80, "https": 443}}
var inferedHostWorkloadEntry = &v1alpha3.WorkloadEntry{Address: cname, Ports: map[string]uint32{"http": 80, "https": 443}}

var goldenPathListNamespaces = servicediscovery.ListNamespacesOutput{
	Namespaces: []sdTypes.NamespaceSummary{
		{Id: &hostname, Name: &hostname},
	},
}
var goldenPathListServices = servicediscovery.ListServicesOutput{
	Services: []sdTypes.ServiceSummary{
		{Name: &subdomain},
	},
}
var goldenPathDiscoverInstances = servicediscovery.DiscoverInstancesOutput{
	Instances: []sdTypes.HttpInstanceSummary{
		{Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41}},
	},
}

func TestWatcher_refreshCache(t *testing.T) {
	tests := []struct {
		name        string
		listNsRes   *servicediscovery.ListNamespacesOutput
		listNsErr   error
		listSvcRes  *servicediscovery.ListServicesOutput
		listSvcErr  error
		discInstRes *servicediscovery.DiscoverInstancesOutput
		discInstErr error
		want        map[string][]*v1alpha3.WorkloadEntry
	}{
		{
			name:        "store gets updated",
			listNsRes:   &goldenPathListNamespaces,
			listSvcRes:  &goldenPathListServices,
			discInstRes: &goldenPathDiscoverInstances,
			want:        map[string][]*v1alpha3.WorkloadEntry{"demo.tetrate.io": {inferedIPv41WorkloadEntry}},
		},
		{
			name:      "store unchanged on ListNamespace error",
			listNsErr: errors.New("bang"),
			want:      map[string][]*v1alpha3.WorkloadEntry{},
		},
		{
			name:       "store unchanged on ListService error",
			listNsRes:  &goldenPathListNamespaces,
			listSvcErr: errors.New("bang"),
			want:       map[string][]*v1alpha3.WorkloadEntry{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &mockSDAPI{
				ListNsResult: tt.listNsRes, ListNsErr: tt.listNsErr,
				ListSvcResult: tt.listSvcRes, ListSvcErr: tt.listSvcErr,
				DiscInstResult: tt.discInstRes, DiscInstErr: tt.discInstErr,
			}
			w := &watcher{cloudmap: mockAPI, store: provider.NewStore()}
			w.refreshStore(context.TODO())
			if !reflect.DeepEqual(w.store.Hosts(), tt.want) {
				t.Errorf("Watcher.store = %v, want %v", w.store.Hosts(), tt.want)
			}
		})
	}
}

func TestWatcher_hostsForNamespace(t *testing.T) {
	tests := []struct {
		name        string
		want        map[string][]*v1alpha3.WorkloadEntry
		ns          sdTypes.NamespaceSummary
		listSvcRes  *servicediscovery.ListServicesOutput
		listSvcErr  error
		discInstRes *servicediscovery.DiscoverInstancesOutput
		discInstErr error
		wantErr     bool
	}{
		{
			name:        "returns hosts for the given namespace",
			ns:          sdTypes.NamespaceSummary{Id: &hostname, Name: &hostname},
			listSvcRes:  &goldenPathListServices,
			discInstRes: &goldenPathDiscoverInstances,
			want:        map[string][]*v1alpha3.WorkloadEntry{"demo.tetrate.io": {inferedIPv41WorkloadEntry}},
		},
		{
			name:       "returns host with host as workload entry if host exists but has no Workload Entries",
			ns:         sdTypes.NamespaceSummary{Id: &hostname, Name: &hostname},
			listSvcRes: &goldenPathListServices,
			discInstRes: &servicediscovery.DiscoverInstancesOutput{
				Instances: []sdTypes.HttpInstanceSummary{},
			},
			want: map[string][]*v1alpha3.WorkloadEntry{"demo.tetrate.io": {inferedHostWorkloadEntry}},
		},
		{
			name:        "errors if DiscoverInstances errors",
			ns:          sdTypes.NamespaceSummary{Id: &hostname, Name: &hostname},
			listSvcRes:  &goldenPathListServices,
			discInstErr: errors.New("bang"),
			wantErr:     true,
		},
		{
			name:       "errors if ListServices errors",
			ns:         sdTypes.NamespaceSummary{Id: &hostname, Name: &hostname},
			listSvcErr: errors.New("bang"),
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &mockSDAPI{
				DiscInstResult: tt.discInstRes, DiscInstErr: tt.discInstErr,
				ListSvcResult: tt.listSvcRes, ListSvcErr: tt.listSvcErr,
			}
			w := &watcher{cloudmap: mockAPI}
			got, err := w.hostsForNamespace(context.TODO(), &tt.ns)
			if (err != nil) != tt.wantErr {
				t.Errorf("Watcher.hostsForNamespace() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Watcher.hostsForNamespace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWatcher_workloadEntriesForService(t *testing.T) {
	tests := []struct {
		name        string
		svc         sdTypes.ServiceSummary
		ns          sdTypes.NamespaceSummary
		discInstRes *servicediscovery.DiscoverInstancesOutput
		discInstErr error
		want        []*v1alpha3.WorkloadEntry
		wantErr     bool
	}{
		{
			name:        "Returns Workload Entries for service",
			discInstRes: &goldenPathDiscoverInstances,
			svc:         sdTypes.ServiceSummary{Name: &subdomain},
			ns:          sdTypes.NamespaceSummary{Name: &hostname},
			want:        []*v1alpha3.WorkloadEntry{inferedIPv41WorkloadEntry},
		},
		{
			name:        "Returns Workload Entries for service if zero instances",
			discInstRes: &servicediscovery.DiscoverInstancesOutput{Instances: []sdTypes.HttpInstanceSummary{}},
			svc:         sdTypes.ServiceSummary{Name: &subdomain},
			ns:          sdTypes.NamespaceSummary{Name: &hostname},
			want:        []*v1alpha3.WorkloadEntry{inferedHostWorkloadEntry},
		},
		{
			name:        "Errors if call to DiscoverInstances errors",
			discInstErr: errors.New("bang"),
			svc:         sdTypes.ServiceSummary{Name: &subdomain},
			ns:          sdTypes.NamespaceSummary{Name: &hostname},
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAPI := &mockSDAPI{DiscInstResult: tt.discInstRes, DiscInstErr: tt.discInstErr}
			w := &watcher{cloudmap: mockAPI}
			got, err := w.workloadEntriesForService(context.TODO(), &tt.svc, &tt.ns)
			if (err != nil) != tt.wantErr {
				t.Errorf("Watcher.workloadEntriesForService() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Watcher.workloadEntriesForService() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_instancesToWorkloadEntries(t *testing.T) {
	tests := []struct {
		name      string
		instances []sdTypes.HttpInstanceSummary
		want      []*v1alpha3.WorkloadEntry
	}{
		{
			name: "Handles multiple instances of the same type",
			instances: []sdTypes.HttpInstanceSummary{
				{Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41}},
				{Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv42}},
			},
			want: []*v1alpha3.WorkloadEntry{inferedIPv41WorkloadEntry, inferedIPv42WorkloadEntry},
		},
		{
			name: "Handles multiple instances of differing type",
			instances: []sdTypes.HttpInstanceSummary{
				{Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41}},
				{
					InstanceId: &subdomain, ServiceName: &subdomain, NamespaceName: &hostname,
					Attributes: map[string]string{"AWS_ALIAS_DNS_NAME": hostname},
				},
			},
			want: []*v1alpha3.WorkloadEntry{inferedIPv41WorkloadEntry},
		},
		{
			name: "handles empty instance attributes map",
			instances: []sdTypes.HttpInstanceSummary{
				{
					InstanceId: &subdomain, ServiceName: &subdomain, NamespaceName: &hostname,
					Attributes: map[string]string{},
				},
			},
			want: []*v1alpha3.WorkloadEntry{},
		},
		{
			name:      "Handles empty instances slice",
			instances: []sdTypes.HttpInstanceSummary{},
			want:      []*v1alpha3.WorkloadEntry{},
		},
		{
			name:      "Handles nil instances slice",
			instances: nil,
			want:      []*v1alpha3.WorkloadEntry{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := instancesToWorkloadEntries(tt.instances); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("instancesToWorkloadEntries() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_instanceToWorkloadEntry(t *testing.T) {
	tests := []struct {
		name     string
		instance *sdTypes.HttpInstanceSummary
		want     *v1alpha3.WorkloadEntry
	}{
		{
			name: "Workload Entry from AWS_INSTANCE_IPV4 instance with AWS_INSTANCE_PORT set to known proto",
			instance: &sdTypes.HttpInstanceSummary{
				Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41, "AWS_INSTANCE_PORT": httpPortStr},
			},
			want: &v1alpha3.WorkloadEntry{Address: ipv41, Ports: map[string]uint32{"http": 80}},
		},
		{
			name: "Workload Entry from AWS_INSTANCE_CNAME instance with AWS_INSTANCE_PORT set to known proto",
			instance: &sdTypes.HttpInstanceSummary{
				Attributes: map[string]string{"AWS_INSTANCE_CNAME": cname, "AWS_INSTANCE_PORT": httpPortStr},
			},
			want: &v1alpha3.WorkloadEntry{Address: cname, Ports: map[string]uint32{"http": 80}},
		},
		{
			name: "Workload Entry from AWS_INSTANCE_IPV4 instance with AWS_INSTANCE_PORT set to unknown proto",
			instance: &sdTypes.HttpInstanceSummary{
				Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41, "AWS_INSTANCE_PORT": portStr},
			},
			want: &v1alpha3.WorkloadEntry{Address: ipv41, Ports: map[string]uint32{"tcp": 9999}},
		},
		{
			name: "Workload Entry from AWS_INSTANCE_CNAME instance with AWS_INSTANCE_PORT set to unknown proto",
			instance: &sdTypes.HttpInstanceSummary{
				Attributes: map[string]string{"AWS_INSTANCE_CNAME": cname, "AWS_INSTANCE_PORT": portStr},
			},
			want: &v1alpha3.WorkloadEntry{Address: cname, Ports: map[string]uint32{"tcp": 9999}},
		},
		{
			name: "Workload Entry infering http and https from AWS_INSTANCE_IPV4 instance without a port",
			instance: &sdTypes.HttpInstanceSummary{
				Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41},
			},
			want: inferedIPv41WorkloadEntry,
		},
		{
			name: "Workload Entry infering http and https from AWS_INSTANCE_IPV4 instance with non-int port",
			instance: &sdTypes.HttpInstanceSummary{
				Attributes: map[string]string{"AWS_INSTANCE_IPV4": ipv41, "AWS_INSTANCE_PORT": hostname},
			},
			want: inferedIPv41WorkloadEntry,
		},
		{
			name: "Nil for instance with AWS_ALIAS_DNS_NAME",
			instance: &sdTypes.HttpInstanceSummary{
				InstanceId: &subdomain, ServiceName: &subdomain, NamespaceName: &hostname,
				Attributes: map[string]string{"AWS_ALIAS_DNS_NAME": hostname},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := instanceToWorkloadEntry(tt.instance); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("instanceToWorkloadEntry() = %v, want %v", got, tt.want)
			}
		})
	}
}
