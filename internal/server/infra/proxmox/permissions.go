// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

// requiredPermissions lists the Proxmox VE API token privileges required for
// VM lifecycle management. These map to the recommended role assignment:
// PVEVMAdmin + PVEDatastoreAdmin + PVEAuditor + PVESDNUser on /.
//
// See Proxmox docs for the full privilege-to-API mapping.
var requiredPermissions = []string{
	// PVEAuditor
	"Sys.Audit",

	// PVEVMAdmin
	"VM.Allocate",
	"VM.Audit",
	"VM.Clone",
	"VM.PowerMgmt",
	"VM.Config.CPU",
	"VM.Config.Memory",
	"VM.Config.Network",
	"VM.Config.Disk",
	"VM.Config.CDROM",
	"VM.Config.Options",

	// PVEDatastoreAdmin
	"Datastore.Audit",
	"Datastore.Allocate",
	"Datastore.AllocateSpace",
	"Datastore.AllocateTemplate",

	// PVESDNUser
	"SDN.Use",
}
