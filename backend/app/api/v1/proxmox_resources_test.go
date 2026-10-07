package v1

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// TestListVLANTrunkMappingsLinksExposedVNets verifies a trunk's consumer mappings include the owning deployment and resource IDs.
func TestListVLANTrunkMappingsLinksExposedVNets(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("get administrator: %v", err)
	}
	var deployment *db.Deployment
	if deployment, err = domain.New(store).CreateDeployment(admin.ID, "vlan-lab", ""); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var root *db.OwnershipNode
	if root, err = store.OwnershipNodes.Select(*deployment.RootNodeID); err != nil {
		t.Fatalf("load deployment root: %v", err)
	}
	var owner *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID, ParentID: &root.ID, Kind: db.OwnershipNodeKindResource,
		Name: "shared", CreatedAt: time.Now(),
	}
	if err = store.OwnershipNodes.Insert(owner); err != nil {
		t.Fatalf("create VNet owner: %v", err)
	}
	var configuration []byte
	if configuration, err = json.Marshal(domain.ManagedNetworkConfiguration{Request: proxmox.SDNNetworkRequest{
		ExternalVLAN: &proxmox.SDNExternalVLANExposure{
			TrunkNode: "tungsten", TrunkBridge: "ogtrunk", VLANID: 2048, Nodes: []string{"osmium", "tungsten"},
		},
	}}); err != nil {
		t.Fatalf("encode VNet configuration: %v", err)
	}
	var resource *db.ManagedResource = &db.ManagedResource{
		DeploymentID: deployment.ID, OwnershipID: owner.ID, Kind: "virtual_network", Name: "shared",
		ConfigurationJSON: string(configuration), CreatedAt: time.Now(),
	}
	if err = store.ManagedResources.Insert(resource); err != nil {
		t.Fatalf("create VNet resource: %v", err)
	}

	var mappings []vlanTrunkMapping
	if mappings, err = listVLANTrunkMappings(store); err != nil {
		t.Fatalf("list mappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want only the selected trunk node: %#v", len(mappings), mappings)
	}
	if mappings[0].Node != "tungsten" {
		t.Fatalf("mapping did not use selected trunk node: %#v", mappings)
	}
	for _, mapping := range mappings {
		if mapping.DeploymentID != deployment.ID || mapping.ResourceID != resource.ID || mapping.DeploymentName != deployment.Name || mapping.VNetName != "shared" || mapping.Bridge != "ogtrunk" || mapping.VLANID != 2048 {
			t.Fatalf("mapping did not identify its consumer: %#v", mapping)
		}
	}
}
