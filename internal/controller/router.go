package controller

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/config"
	computehandler "github.com/agomez/pulsar/internal/controller/compute"
	identityhandler "github.com/agomez/pulsar/internal/controller/identity"
	imagehandler "github.com/agomez/pulsar/internal/controller/image"
	keypairhandler "github.com/agomez/pulsar/internal/controller/keypair"
	networkhandler "github.com/agomez/pulsar/internal/controller/network"
	"github.com/agomez/pulsar/internal/controller/registry"
	storagehandler "github.com/agomez/pulsar/internal/controller/storage"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
	ui "github.com/agomez/pulsar/web"
)

func newRouter(
	cfg *config.Config,
	log *zap.Logger,
	store *etcd.Client,
	db *postgres.DB,
	reg *registry.AgentRegistry,
) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(api.ZapMiddleware(log))

	// Health check (unauthenticated)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// Image service (independent)
	imgSvc, err := imagehandler.NewService(store, cfg.Controller.ImageStoreDir)
	if err != nil {
		log.Fatal("failed to initialise image service", zap.Error(err))
	}
	imgHandler := imagehandler.NewHandler(imgSvc, cfg.Controller.PublicURL, log)
	r.Route("/v1/images", func(r chi.Router) {
		// Content download is unauthenticated so agents can fetch without a JWT
		r.Get("/{id}/content", imgHandler.Content)

		r.Group(func(r chi.Router) {
			r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
			r.Use(api.ProjectScopeMiddleware(db))
			r.Get("/", imgHandler.List)
			r.Post("/", imgHandler.Create)
			r.Get("/{id}", imgHandler.Get)
			r.Delete("/{id}", imgHandler.Delete)
		})
	})

	// Identity
	idHandler := identityhandler.NewHandler(cfg, db, store, log)
	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/tokens", idHandler.CreateToken)
		r.Delete("/tokens", idHandler.DeleteToken)
		// Public: used by the login page to discover SSO options for an email address.
		r.Get("/idps", idHandler.LookupIDPs)
		// Personal access tokens (self-service, any authenticated user)
		r.Group(func(r chi.Router) {
			r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
			r.Get("/tokens/personal", idHandler.ListPATs)
			r.Post("/tokens/personal", idHandler.CreatePAT)
			r.Delete("/tokens/personal/{id}", idHandler.DeletePAT)
		})
	})
	// Organizations (platform_admin only, enforced in handler)
	r.Route("/v1/orgs", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Get("/", idHandler.ListOrgs)
		r.Post("/", idHandler.CreateOrg)
		r.Get("/{id}", idHandler.GetOrg)
		r.Patch("/{id}", idHandler.UpdateOrg)
		r.Delete("/{id}", idHandler.DeleteOrg)
	})
	// Identity providers (org_admin or platform_admin, enforced in handler)
	r.Route("/v1/orgs/{org_id}/idps", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Get("/", idHandler.ListIDPs)
		r.Post("/", idHandler.CreateIDP)
		r.Patch("/{idp_id}", idHandler.UpdateIDP)
		r.Delete("/{idp_id}", idHandler.DeleteIDP)
	})
	// OIDC authorize / callback — unauthenticated, browser-facing
	r.Get("/v1/auth/oidc/{idp_slug}/authorize", idHandler.OIDCAuthorize)
	r.Get("/v1/auth/oidc/{idp_slug}/callback", idHandler.OIDCCallback)
	r.Route("/v1/projects", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Get("/", idHandler.ListProjects)
		r.Post("/", idHandler.CreateProject)
		r.Get("/{id}", idHandler.GetProject)
		r.Patch("/{id}", idHandler.UpdateProject)
		r.Delete("/{id}", idHandler.DeleteProject)
	})
	r.Route("/v1/users", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Get("/", idHandler.ListUsers)
		r.Post("/", idHandler.CreateUser)
		r.Patch("/{id}", idHandler.UpdateUser)
		r.Delete("/{id}", idHandler.DeleteUser)
		// Project membership sub-resource (admin only, enforced in handler)
		r.Get("/{id}/projects", idHandler.ListUserProjects)
		r.Put("/{id}/projects/{project_id}", idHandler.GrantProjectAccess)
		r.Delete("/{id}/projects/{project_id}", idHandler.RevokeProjectAccess)
	})

	// Network service (created first so compute can use it)
	netSvc := networkhandler.NewService(store, reg, log)
	reg.RegisterTaskResultHandler("network", netSvc.HandleTaskResult)
	reg.RegisterAgentLostHandler("network", netSvc.HandleAgentLost)

	// Storage service (created before compute so compute can release volumes on delete)
	storSvc := storagehandler.NewService(store, reg, log)
	reg.RegisterTaskResultHandler("storage", storSvc.HandleTaskResult)
	reg.RegisterAgentLostHandler("storage", storSvc.HandleAgentLost)

	// Compute
	compSvc := computehandler.NewService(store, reg, netSvc, storSvc, log)
	reg.RegisterAgentLostHandler("compute", compSvc.HandleAgentLost)
	reg.RegisterAgentReconnectHandler("compute", compSvc.HandleAgentReconnected)
	compHandler := computehandler.NewHandlerWithService(cfg, compSvc, reg, log)

	// Keypairs (stored in Postgres, served under /v1/compute/keypairs)
	kpSvc := keypairhandler.NewService(db)
	kpHandler := keypairhandler.NewHandler(kpSvc, log)
	compSvc.SetKeypairService(kpSvc)

	r.Route("/v1/compute", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Use(api.ProjectScopeMiddleware(db))
		r.Get("/instances", compHandler.ListInstances)
		r.Post("/instances", compHandler.CreateInstance)
		r.Get("/instances/{id}", compHandler.GetInstance)
		r.Delete("/instances/{id}", compHandler.DeleteInstance)
		r.Post("/instances/{id}/action", compHandler.InstanceAction)
		r.Post("/instances/{id}/reset", compHandler.ResetInstance)
		r.Get("/instances/{id}/console", compHandler.GetConsole)
		r.Post("/instances/{id}/migrate", compHandler.MigrateInstance)
		r.Post("/instances/{id}/resize", compHandler.ResizeInstance)
		r.Post("/instances/{id}/interfaces", compHandler.AttachInterface)
		r.Delete("/instances/{id}/interfaces/{port_id}", compHandler.DetachInterface)
		r.Put("/instances/{id}/metadata", compHandler.UpdateMetadata)
		r.Put("/instances/{id}/metadata/{key}", compHandler.SetMetadataKey)
		r.Delete("/instances/{id}/metadata/{key}", compHandler.DeleteMetadataKey)
		r.Get("/flavors", compHandler.ListFlavors)
		r.Post("/flavors", compHandler.CreateFlavor)
		r.Get("/flavors/{id}", compHandler.GetFlavor)
		r.Delete("/flavors/{id}", compHandler.DeleteFlavor)
		r.Get("/nodes", compHandler.ListNodes)
		r.Get("/keypairs", kpHandler.List)
		r.Post("/keypairs", kpHandler.Create)
		r.Get("/keypairs/{id}", kpHandler.Get)
		r.Delete("/keypairs/{id}", kpHandler.Delete)
	})
	// Console WebSocket proxy — token-authenticated, no JWT middleware
	r.Get("/v1/compute/console/ws", compHandler.ConsoleProxy)

	// Network
	netHandler := networkhandler.NewHandler(netSvc, log)
	r.Route("/v1/network", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Use(api.ProjectScopeMiddleware(db))
		r.Get("/networks", netHandler.ListNetworks)
		r.Post("/networks", netHandler.CreateNetwork)
		r.Get("/networks/{id}", netHandler.GetNetwork)
		r.Patch("/networks/{id}", netHandler.UpdateNetwork)
		r.Delete("/networks/{id}", netHandler.DeleteNetwork)
		r.Get("/subnets", netHandler.ListSubnets)
		r.Post("/subnets", netHandler.CreateSubnet)
		r.Get("/subnets/{id}", netHandler.GetSubnet)
		r.Delete("/subnets/{id}", netHandler.DeleteSubnet)
		r.Get("/ports", netHandler.ListPorts)
		r.Post("/ports", netHandler.CreatePort)
		r.Get("/ports/{id}", netHandler.GetPort)
		r.Patch("/ports/{id}", netHandler.UpdatePort)
		r.Delete("/ports/{id}", netHandler.DeletePort)
		r.Get("/routers", netHandler.ListRouters)
		r.Post("/routers", netHandler.CreateRouter)
		r.Get("/routers/{id}", netHandler.GetRouter)
		r.Patch("/routers/{id}", netHandler.UpdateRouter)
		r.Delete("/routers/{id}", netHandler.DeleteRouter)
		r.Put("/routers/{id}/interfaces", netHandler.AddRouterInterface)
		r.Delete("/routers/{id}/interfaces", netHandler.RemoveRouterInterface)
		r.Put("/routers/{id}/gateway", netHandler.SetRouterGateway)
		r.Delete("/routers/{id}/gateway", netHandler.ClearRouterGateway)
		r.Get("/floatingips", netHandler.ListFloatingIPs)
		r.Post("/floatingips", netHandler.CreateFloatingIP)
		r.Get("/floatingips/{id}", netHandler.GetFloatingIP)
		r.Patch("/floatingips/{id}", netHandler.UpdateFloatingIP)
		r.Delete("/floatingips/{id}", netHandler.DeleteFloatingIP)
		r.Get("/security-groups", netHandler.ListSecurityGroups)
		r.Post("/security-groups", netHandler.CreateSecurityGroup)
		r.Get("/security-groups/{id}", netHandler.GetSecurityGroup)
		r.Delete("/security-groups/{id}", netHandler.DeleteSecurityGroup)
		r.Post("/security-groups/{id}/rules", netHandler.AddSecurityGroupRule)
		r.Delete("/security-groups/{id}/rules/{rule_id}", netHandler.DeleteSecurityGroupRule)
		r.Get("/firewall-policies", netHandler.ListFirewallPolicies)
		r.Post("/firewall-policies", netHandler.CreateFirewallPolicy)
		r.Get("/firewall-policies/{id}", netHandler.GetFirewallPolicy)
		r.Patch("/firewall-policies/{id}", netHandler.UpdateFirewallPolicy)
		r.Delete("/firewall-policies/{id}", netHandler.DeleteFirewallPolicy)
	})

	// Storage
	storHandler := storagehandler.NewHandler(storSvc, log)
	r.Route("/v1/storage", func(r chi.Router) {
		r.Use(api.AuthMiddleware(cfg.Controller.JWT.Secret, store, db))
		r.Use(api.ProjectScopeMiddleware(db))
		r.Get("/volumes", storHandler.ListVolumes)
		r.Post("/volumes", storHandler.CreateVolume)
		r.Get("/volumes/{id}", storHandler.GetVolume)
		r.Delete("/volumes/{id}", storHandler.DeleteVolume)
		r.Post("/volumes/{id}/action", storHandler.VolumeAction)
		r.Get("/snapshots", storHandler.ListSnapshots)
		r.Post("/snapshots", storHandler.CreateSnapshot)
		r.Get("/snapshots/{id}", storHandler.GetSnapshot)
		r.Delete("/snapshots/{id}", storHandler.DeleteSnapshot)
		r.Get("/volume-types", storHandler.ListVolumeTypes)
		r.Post("/volume-types", storHandler.CreateVolumeType)
		r.Get("/volume-types/{id}", storHandler.GetVolumeType)
		r.Delete("/volume-types/{id}", storHandler.DeleteVolumeType)
	})

	// Serve embedded React UI with SPA fallback
	staticFS, err := fs.Sub(ui.FS, "dist")
	if err != nil {
		log.Fatal("failed to sub embed FS", zap.Error(err))
	}
	fileServer := http.FileServer(http.FS(staticFS))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := staticFS.Open(path)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return r
}
