// Package dockerhost owns host-wide Docker reports and explicitly confirmed pruning.
package dockerhost

import (
	"context"
	"errors"
)

var ErrPruneUncertain = errors.New("Docker prune completion is unknown")
var ErrMaintenanceBlocked = errors.New("Docker operations are paused because a previous prune may still be deleting. Check the Docker daemon, then explicitly resume Docker operations")

type Kind string

const (
	Images       Kind = "images"
	UnusedImages Kind = "unused-images"
	Containers   Kind = "containers"
	Networks     Kind = "networks"
	BuildCache   Kind = "build-cache"
	Volumes      Kind = "volumes"
	AllVolumes   Kind = "all-volumes"
)

func (k Kind) Confirmation() string {
	switch k {
	case Volumes, AllVolumes:
		return "DELETE VOLUMES"
	case Images, UnusedImages, Containers, Networks, BuildCache:
		return "PRUNE"
	default:
		return ""
	}
}

// PanelDataConfirmation is the word that includes panel-owned volumes and
// images in a prune, in place of the kind's usual Confirmation.
const PanelDataConfirmation = "DELETE PANEL DATA"

// Listed reports whether the kind goes through Candidates, Plan and Remove,
// so panel-owned resources can be skipped, rather than Docker's host-wide Prune.
func (k Kind) Listed() bool {
	switch k {
	case Volumes, AllVolumes, Images, UnusedImages:
		return true
	}
	return false
}

type Engine interface {
	Report(context.Context) (Report, error)
	// Prune is Docker's own prune, for the kinds that are not Listed.
	Prune(context.Context, Kind) (PruneResult, error)
	// Candidates lists what a Listed kind would remove: Docker's unused
	// volumes or images, narrowed to the kind's meaning.
	Candidates(ctx context.Context, kind Kind) ([]Candidate, error)
	// Remove deletes one candidate without force. refusal is non-empty when
	// Docker said no (in use, conflict); err is a transport or unknown failure.
	Remove(ctx context.Context, kind Kind, c Candidate) (removed bool, refusal string, err error)
}

type Report struct {
	MaintenanceBlocked bool        `json:"maintenanceBlocked"`
	CheckedAt          string      `json:"checkedAt"`
	Version            string      `json:"version"`
	APIVersion         string      `json:"apiVersion"`
	Hostname           string      `json:"hostname"`
	StorageDriver      string      `json:"storageDriver"`
	DockerRootDir      string      `json:"dockerRootDir"`
	ContainersRunning  int         `json:"containersRunning"`
	ContainersPaused   int         `json:"containersPaused"`
	ContainersStopped  int         `json:"containersStopped"`
	Usage              []Usage     `json:"usage"`
	Containers         []Container `json:"containers"`
	Volumes            []Volume    `json:"volumes"`
	Networks           []Network   `json:"networks"`
	Warnings           []string    `json:"warnings"`
}
type Usage struct {
	Kind        string  `json:"kind"`
	Count       int     `json:"count"`
	Active      int     `json:"active"`
	Size        *uint64 `json:"size"`
	Reclaimable *uint64 `json:"reclaimable"`
}
type Container struct {
	ID    string   `json:"id"`
	Names []string `json:"names"`
	Image string   `json:"image"`
	State string   `json:"state"`
	Size  *uint64  `json:"size"`
}
type Volume struct {
	Name       string  `json:"name"`
	Driver     string  `json:"driver"`
	Size       *uint64 `json:"size"`
	References *int64  `json:"references"`
}
type Network struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Containers *int   `json:"containers"`
}
type PruneResult struct {
	Kind           Kind     `json:"kind"`
	SpaceReclaimed uint64   `json:"spaceReclaimed"`
	Deleted        []string `json:"deleted"`
	// Skipped is what a Listed prune left alone: panel-owned resources and
	// anything Docker refused to remove.
	Skipped []SkipEntry `json:"skipped"`
}
