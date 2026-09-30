// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package health

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// bpfObjectAttrSize is the Linux ABI size through path_fd, excluding Go padding.
const bpfObjectAttrSize = 20

// bpfObjectAttr matches the BPF_OBJ_GET fields of union bpf_attr.
type bpfObjectAttr struct {
	Pathname  uint64
	FD        uint32
	FileFlags uint32
	PathFD    uint32
}

// bpfElementAttr matches the map element fields of union bpf_attr.
type bpfElementAttr struct {
	MapFD uint32
	_     uint32
	Key   uint64
	Value uint64
	Flags uint64
}

// bpfInfoAttr matches the BPF_OBJ_GET_INFO_BY_FD fields of union bpf_attr.
type bpfInfoAttr struct {
	FD      uint32
	InfoLen uint32
	Info    uint64
}

// bpfMapInfo contains the stable prefix of struct bpf_map_info used here.
type bpfMapInfo struct {
	Type       uint32
	ID         uint32
	KeySize    uint32
	ValueSize  uint32
	MaxEntries uint32
	MapFlags   uint32
	Name       [16]byte
}

// bpf invokes the Linux BPF syscall with attr.
func bpf(command int, attr unsafe.Pointer, size uintptr) (uintptr, error) {
	result, _, errno := unix.Syscall(unix.SYS_BPF, uintptr(command), uintptr(attr), size)
	if errno != 0 {
		return 0, errno
	}
	return result, nil
}

// openPinnedBPFObject opens one bpffs object with the requested access flags.
func openPinnedBPFObject(path string, flags uint32) (int, error) {
	pathname, err := unix.BytePtrFromString(path)
	if err != nil {
		return -1, err
	}
	attr := bpfObjectAttr{
		Pathname:  uint64(uintptr(unsafe.Pointer(pathname))),
		FileFlags: flags,
	}
	result, err := bpf(unix.BPF_OBJ_GET, unsafe.Pointer(&attr), bpfObjectAttrSize)
	runtime.KeepAlive(pathname)
	if err != nil {
		return -1, err
	}
	return int(result), nil
}

// validatePinnedBPFLink verifies that path is a kernel-owned object in bpffs.
func validatePinnedBPFLink(path string) error {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("not a pinned BPF object")
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return err
	}
	if filesystem.Type != unix.BPF_FS_MAGIC {
		return fmt.Errorf("not on bpffs")
	}
	return nil
}

// readPinnedEBPFCounters opens and reads the pinned map without write access.
func readPinnedEBPFCounters(path string) (map[uint32]uint64, error) {
	fd, err := openPinnedBPFObject(path, unix.BPF_F_RDONLY)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd) //nolint:errcheck // A read result is not invalidated by a close error.

	var info bpfMapInfo
	attr := bpfInfoAttr{
		FD:      uint32(fd),
		InfoLen: uint32(unsafe.Sizeof(info)),
		Info:    uint64(uintptr(unsafe.Pointer(&info))),
	}
	if _, err := bpf(unix.BPF_OBJ_GET_INFO_BY_FD, unsafe.Pointer(&attr), unsafe.Sizeof(attr)); err != nil {
		return nil, fmt.Errorf("inspect map: %w", err)
	}
	runtime.KeepAlive(&info)
	if info.Type != unix.BPF_MAP_TYPE_HASH || info.KeySize != 4 || info.ValueSize != 8 {
		return nil, fmt.Errorf("unexpected map type/key/value sizes %d/%d/%d", info.Type, info.KeySize, info.ValueSize)
	}

	counters := make(map[uint32]uint64)
	var key uint32
	var keyPointer uint64
	for {
		var next uint32
		nextAttr := bpfElementAttr{
			MapFD: uint32(fd),
			Key:   keyPointer,
			Value: uint64(uintptr(unsafe.Pointer(&next))),
		}
		if _, err := bpf(unix.BPF_MAP_GET_NEXT_KEY, unsafe.Pointer(&nextAttr), unsafe.Sizeof(nextAttr)); err != nil {
			if errors.Is(err, unix.ENOENT) {
				return counters, nil
			}
			return nil, fmt.Errorf("get next key: %w", err)
		}
		runtime.KeepAlive(&key)
		runtime.KeepAlive(&next)

		var value uint64
		lookupAttr := bpfElementAttr{
			MapFD: uint32(fd),
			Key:   uint64(uintptr(unsafe.Pointer(&next))),
			Value: uint64(uintptr(unsafe.Pointer(&value))),
		}
		if _, err := bpf(unix.BPF_MAP_LOOKUP_ELEM, unsafe.Pointer(&lookupAttr), unsafe.Sizeof(lookupAttr)); err != nil {
			return nil, fmt.Errorf("look up port %d: %w", next, err)
		}
		runtime.KeepAlive(&next)
		runtime.KeepAlive(&value)
		counters[next] = value
		key = next
		keyPointer = uint64(uintptr(unsafe.Pointer(&key)))
	}
}
