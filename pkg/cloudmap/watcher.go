package cloudmap

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdTypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
	"github.com/pkg/errors"
	"istio.io/api/networking/v1alpha3"

	"github.com/tetratelabs/istio-cloud-map/pkg/infer"
	"github.com/tetratelabs/istio-cloud-map/pkg/provider"
	"github.com/tetratelabs/log"
)

// consts aren't memory addressable in Go
var serviceFilterNamespaceID = sdTypes.ServiceFilterNameNamespaceId
var filterConditionEquals = sdTypes.FilterConditionEq

// NewWatcher returns a Cloud Map watcher
func NewWatcher(ctx context.Context, store provider.Store, region, id, secret string) (provider.Watcher, error) {
	if len(region) == 0 {
		var ok bool
		if region, ok = os.LookupEnv("AWS_REGION"); !ok {
			return nil, errors.New("AWS region must be specified")
		}
	}
	var cfg aws.Config
	var err error
	if len(id) != 0 && len(secret) != 0 {
		// Use AWS id and secret from CLI parameters
		creds := aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(id, secret, ""))
		cfg, err = config.LoadDefaultConfig(ctx, config.WithCredentialsProvider(creds), config.WithRegion(region))
	} else {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
	}
	if err != nil {
		return nil, errors.Wrap(err, "error loading AWS config")
	}
	sdclient := servicediscovery.NewFromConfig(cfg)
	return &watcher{cloudmap: sdclient, store: store, interval: time.Second * 5}, nil
}

type ServiceDiscoveryClient interface {
	DiscoverInstances(ctx context.Context, params *servicediscovery.DiscoverInstancesInput, optFns ...func(*servicediscovery.Options)) (*servicediscovery.DiscoverInstancesOutput, error)
	ListNamespaces(ctx context.Context, params *servicediscovery.ListNamespacesInput, optFns ...func(*servicediscovery.Options)) (*servicediscovery.ListNamespacesOutput, error)
	ListServices(ctx context.Context, params *servicediscovery.ListServicesInput, optFns ...func(*servicediscovery.Options)) (*servicediscovery.ListServicesOutput, error)
}

// watcher polls Cloud Map and caches a list of services and their instances

type watcher struct {
	cloudmap ServiceDiscoveryClient
	store    provider.Store
	interval time.Duration
}

var _ provider.Watcher = &watcher{}

func (w *watcher) Store() provider.Store {
	return w.store
}

func (w *watcher) Prefix() string {
	return "cloudmap-"
}

// Run the watcher until the context is cancelled
func (w *watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Initial sync on startup
	w.refreshStore(ctx)
	for {
		select {
		case <-ticker.C:
			w.refreshStore(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (w *watcher) refreshStore(ctx context.Context) {
	log.Info("Syncing Cloud Map store")
	// TODO: allow users to specify namespaces to watch
	nsResp, err := w.cloudmap.ListNamespaces(ctx, &servicediscovery.ListNamespacesInput{})
	if err != nil {
		log.Errorf("error retrieving namespace list from Cloud Map: %v", err)
		return
	}
	// We want to continue to use existing store on error
	tempStore := map[string][]*v1alpha3.WorkloadEntry{}
	for _, ns := range nsResp.Namespaces {
		hosts, err := w.hostsForNamespace(ctx, &ns)
		if err != nil {
			log.Errorf("unable to refresh Cloud Map cache due to error, using existing cache: %v", err)
			return
		}
		// Hosts are "svcName.nsName" so by definition can't be the same across namespaces or services
		for host, eps := range hosts {
			tempStore[host] = eps
		}
	}
	log.Info("Cloud Map store sync successful")
	w.store.Set(tempStore)
}

func (w *watcher) hostsForNamespace(ctx context.Context, ns *sdTypes.NamespaceSummary) (map[string][]*v1alpha3.WorkloadEntry, error) {
	hosts := map[string][]*v1alpha3.WorkloadEntry{}
	svcResp, err := w.cloudmap.ListServices(ctx, &servicediscovery.ListServicesInput{
		Filters: []sdTypes.ServiceFilter{
			{
				Name:      serviceFilterNamespaceID,
				Values:    []string{*ns.Id},
				Condition: filterConditionEquals,
			},
		},
	})
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving service list from Cloud Map for namespace %q", *ns.Name)
	}
	for _, svc := range svcResp.Services {
		host := fmt.Sprintf("%v.%v", *svc.Name, *ns.Name)
		wes, err := w.workloadEntriesForService(ctx, &svc, ns)
		if err != nil {
			return nil, err
		}
		log.Infof("%v Workload Entries found for %q", len(wes), host)
		hosts[host] = wes
	}
	return hosts, nil
}

func (w *watcher) workloadEntriesForService(ctx context.Context, svc *sdTypes.ServiceSummary, ns *sdTypes.NamespaceSummary) ([]*v1alpha3.WorkloadEntry, error) {
	// TODO: use health filter?
	instOutput, err := w.cloudmap.DiscoverInstances(ctx, &servicediscovery.DiscoverInstancesInput{ServiceName: svc.Name, NamespaceName: ns.Name})
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving instance list from Cloud Map for %q in %q", *svc.Name, *ns.Name)
	}
	// Inject host based instance if there are no instances
	if len(instOutput.Instances) == 0 {
		host := fmt.Sprintf("%v.%v", *svc.Name, *ns.Name)
		instOutput.Instances = []sdTypes.HttpInstanceSummary{
			{Attributes: map[string]string{"AWS_INSTANCE_CNAME": host}},
		}
	}
	return instancesToWorkloadEntries(instOutput.Instances), nil
}

func instancesToWorkloadEntries(instances []sdTypes.HttpInstanceSummary) []*v1alpha3.WorkloadEntry {
	wes := make([]*v1alpha3.WorkloadEntry, 0, len(instances))
	for _, inst := range instances {
		we := instanceToWorkloadEntry(&inst)
		if we != nil {
			wes = append(wes, we)
		}
	}
	return wes
}

func instanceToWorkloadEntry(instance *sdTypes.HttpInstanceSummary) *v1alpha3.WorkloadEntry {
	var address string
	if ip, ok := instance.Attributes["AWS_INSTANCE_IPV4"]; ok {
		address = ip
	} else if cname, ok := instance.Attributes["AWS_INSTANCE_CNAME"]; ok {
		address = cname
	}
	if address == "" {
		log.Infof("instance %v of %v.%v is of a type that is not currently supported", *instance.InstanceId, *instance.ServiceName, *instance.NamespaceName)
		return nil
	}
	if port, ok := instance.Attributes["AWS_INSTANCE_PORT"]; ok {
		p, err := strconv.Atoi(port)
		if err == nil {
			return infer.WorkloadEntry(address, uint32(p))
		}
		log.Errorf("error converting Port string %v to int: %v", port, err)
	}
	log.Infof("no port found for address %v, assuming http (80) and https (443)", address)
	return &v1alpha3.WorkloadEntry{Address: address, Ports: map[string]uint32{"http": 80, "https": 443}}
}
