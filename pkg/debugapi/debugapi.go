package debugapi

import (
	"context"
	"crypto/ecdsa"
	"net/http"
	"sync"

	"github.com/FavorLabs/favorX/pkg/address"
	"github.com/FavorLabs/favorX/pkg/addressbook"
	"github.com/FavorLabs/favorX/pkg/boson"
	"github.com/FavorLabs/favorX/pkg/chunkinfo"
	"github.com/FavorLabs/favorX/pkg/fileinfo"
	"github.com/FavorLabs/favorX/pkg/logging"
	"github.com/FavorLabs/favorX/pkg/multicast"
	"github.com/FavorLabs/favorX/pkg/p2p"
	"github.com/FavorLabs/favorX/pkg/pingpong"
	"github.com/FavorLabs/favorX/pkg/retrieval"
	"github.com/FavorLabs/favorX/pkg/routetab"
	"github.com/FavorLabs/favorX/pkg/settlement/traffic"
	"github.com/FavorLabs/favorX/pkg/storage"
	"github.com/FavorLabs/favorX/pkg/topology"
	"github.com/FavorLabs/favorX/pkg/topology/bootnode"
	"github.com/FavorLabs/favorX/pkg/topology/lightnode"
	"github.com/FavorLabs/favorX/pkg/tracing"
	"github.com/gogf/gf/v2/os/gcache"
	"github.com/gogf/gf/v2/os/gctx"
	"github.com/prometheus/client_golang/prometheus"
)

type authenticator interface {
	Authorize(string) bool
	GenerateKey(string, int) (string, error)
	Enforce(string, string, string) (bool, error)
}

// Service implements http.Handler interface to be used in HTTP server.
type Service struct {
	restricted         bool
	auth               authenticator
	overlay            boson.Address
	publicKey          ecdsa.PublicKey
	p2p                p2p.DebugService
	pingpong           pingpong.Interface
	topologyDriver     topology.Driver
	group              *multicast.Service
	storer             storage.Storer
	logger             logging.Logger
	tracer             *tracing.Tracer
	lightNodes         *lightnode.Container
	bootNodes          *bootnode.Container
	routetab           routetab.RouteTab
	chunkInfo          chunkinfo.Interface
	fileInfo           fileinfo.Interface
	retrieval          retrieval.Interface
	traffic            traffic.ApiInterface
	corsAllowedOrigins []string
	metricsRegistry    *prometheus.Registry
	handler            http.Handler
	handlerMu          sync.RWMutex
	nodeOptions        Options
	cache              *gcache.Cache
	cacheCtx           context.Context
	addressBook        addressbook.Interface
}

type Options struct {
	DataDir        string
	NetworkID      uint64
	NATAddr        string
	EnableWS       bool
	EnableQUIC     bool
	NodeMode       address.Model
	LightNodeLimit int
	WelcomeMessage string
	Transaction    []byte
}

// New creates a new Debug API Service with only basic routers enabled in order
// to expose /addresses, /health endpoints, Go metrics and pprof. It is useful to expose
// these endpoints before all dependencies are configured and injected to have
// access to basic debugging tools and /health endpoint.
func New(overlay boson.Address, publicKey ecdsa.PublicKey, logger logging.Logger, tracer *tracing.Tracer, corsAllowedOrigins []string, restrict bool, auth authenticator, o Options) *Service {
	s := new(Service)
	s.auth = auth
	s.restricted = restrict
	s.overlay = overlay
	s.publicKey = publicKey
	s.logger = logger
	s.tracer = tracer
	s.corsAllowedOrigins = corsAllowedOrigins
	s.metricsRegistry = newMetricsRegistry()
	s.nodeOptions = o
	s.cache = gcache.New()
	s.cacheCtx = gctx.New()
	s.setRouter(s.newBasicRouter())

	return s
}

// Configure injects required dependencies and configuration parameters and
// constructs HTTP routes that depend on them. It is intended and safe to call
// this method only once.
func (s *Service) Configure(p2p p2p.DebugService, pingpong pingpong.Interface, group *multicast.Service, topologyDriver topology.Driver, lightNodes *lightnode.Container,
	bootNodes *bootnode.Container, storer storage.Storer, route routetab.RouteTab, chunkinfo chunkinfo.Interface, fileInfo fileinfo.Interface, retrieval retrieval.Interface,
	ab addressbook.Interface) {
	s.p2p = p2p
	s.pingpong = pingpong
	s.topologyDriver = topologyDriver
	s.group = group
	s.storer = storer
	s.lightNodes = lightNodes
	s.bootNodes = bootNodes
	s.routetab = route
	s.chunkInfo = chunkinfo
	s.fileInfo = fileInfo
	s.retrieval = retrieval
	s.addressBook = ab

	s.setRouter(s.newRouter())
}

// ServeHTTP implements http.Handler interface.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// protect handler as it is changed by the Configure method
	s.handlerMu.RLock()
	h := s.handler
	s.handlerMu.RUnlock()

	h.ServeHTTP(w, r)
}
