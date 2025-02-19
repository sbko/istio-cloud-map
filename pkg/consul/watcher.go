package consul

import (
	"context"
	"net/url"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	"istio.io/api/networking/v1alpha3"

	"github.com/tetratelabs/istio-registry-sync/pkg/infer"
	"github.com/tetratelabs/istio-registry-sync/pkg/provider"
	"github.com/tetratelabs/log"
)

var errIndexChangeTimeout = errors.New("blocking request timeout while waiting for index to change")

type watcher struct {
	client       *api.Client
	store        provider.Store
	tickInterval time.Duration
	lastIndex    uint64 // lastly synced index of Catalog
	namespace    string
}

const (
	// TODO: allow users to specify these
	defaultBlockingRequestWaitTimeDuration = 5 * time.Second
	defaultTickIntervalDuration            = 10 * time.Second
)

var _ provider.Watcher = &watcher{}

func NewWatcher(store provider.Store, endpoint string, namespace string) (provider.Watcher, error) {
	if len(endpoint) == 0 {
		return nil, errors.New("Consul endpoint not specified")
	}

	config := api.DefaultConfig()
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing endpoint: %s", endpoint)
	}

	// TODO: allow users to specify TOKEN
	config.Scheme = u.Scheme
	config.Address = u.Host
	config.WaitTime = defaultBlockingRequestWaitTimeDuration

	client, err := api.NewClient(config)
	if err != nil {
		return nil, errors.Wrap(err, "error creating client")
	}
	return &watcher{client: client,
		store:        store,
		tickInterval: defaultTickIntervalDuration,
		// TODO: Since namespace feature is only available in Enterprise (+1.7.0), we haven't tested yet
		namespace: namespace,
	}, nil
}

func (w *watcher) Store() provider.Store {
	return w.store
}

func (w *watcher) Prefix() string {
	return "consul-"
}

// Run the watcher until the context is cancelled
func (w *watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.tickInterval)
	defer ticker.Stop()

	w.refreshStore() // init
	for {
		select {
		case <-ticker.C:
			w.refreshStore()
		case <-ctx.Done():
			return
		}
	}
}

// fetch services and workload entries from consul catalog and sync them with Store
func (w *watcher) refreshStore() {
	names, err := w.listServices()
	if err == errIndexChangeTimeout {
		log.Infof("waiting for index to change: current index: %d", w.lastIndex)
		return
	} else if err != nil {
		log.Errorf("error listing services from Consul: %v", err)
		return
	}

	css := w.describeServices(names)
	data := make(map[string][]*v1alpha3.WorkloadEntry, len(css))
	for name, cs := range css {
		wes := make([]*v1alpha3.WorkloadEntry, 0, len(cs))
		for _, c := range cs {
			if we := catalogServiceToWorkloadEntry(c); we != nil {
				wes = append(wes, we)
			}
		}
		if len(wes) > 0 {
			data[name] = wes
		}
	}
	w.store.Set(data)
}

// listServices lists services
func (w *watcher) listServices() (map[string][]string, error) {
	data, metadata, err := w.client.Catalog().Services(
		&api.QueryOptions{WaitIndex: w.lastIndex, Namespace: w.namespace},
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list services")
	}

	if w.lastIndex == metadata.LastIndex {
		// this case indicates the request reaches timeout of blocking request
		return nil, errIndexChangeTimeout
	}

	w.lastIndex = metadata.LastIndex
	return data, nil
}

// describeServices gets catalog services for given service names
func (w *watcher) describeServices(names map[string][]string) map[string][]*api.CatalogService {
	ss := make(map[string][]*api.CatalogService, len(names))
	for name := range names { // ignore tags in value
		svcs, err := w.describeService(name)
		if err != nil {
			log.Errorf("error describing service catalog from Consul: %v ", err)
			continue
		}
		ss[name] = svcs
	}
	return ss
}

func (w *watcher) describeService(name string) ([]*api.CatalogService, error) {
	svcs, _, err := w.client.Catalog().Service(name, "", &api.QueryOptions{
		Namespace: w.namespace,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to describe svc: %s", name)
	}
	return svcs, nil
}

// catalogServiceToWorkloadEntry converts catalog service to workload entry
func catalogServiceToWorkloadEntry(c *api.CatalogService) *v1alpha3.WorkloadEntry {
	address := c.Address
	if address == "" {
		log.Infof("instance %s of %s.%v is of a type that is not currently supported",
			c.ServiceID, c.ServiceName, c.Namespace)
		return nil
	}

	port := c.ServicePort
	if port > 0 { // port is optional and defaults to zero
		return infer.WorkloadEntry(address, uint32(port))
	}

	log.Infof("no port found for address %v, assuming http (80) and https (443)", address)
	return &v1alpha3.WorkloadEntry{Address: address, Ports: map[string]uint32{"http": 80, "https": 443}}
}
