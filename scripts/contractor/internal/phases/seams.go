package phases

import "homelab/details/tcp"

// The two things a verb's tofu-facing phases reach that are not a program on
// PATH: a TCP port they wait on, and the hypervisor's API. Named here so a
// test can run a whole verb with neither - the machines' Talos API, the state
// database and the Proxmox datastore are not there to answer one.
//
// Nothing else is replaceable, on purpose. tofu, op, age and rclone are run
// as programs, and a test puts its own on PATH; that exercises the command
// lines the phases really build, which a seam in front of them would not.
var (
	// awaitTCP waits for a port to accept a connection.
	awaitTCP = tcp.Await
	// storedImage finds a disk image on a hypervisor's datastore.
	storedImage = findStoredImage
	// removeImage deletes one from it.
	removeImage = deleteDatastoreFile
)
