// Copyright © 2022 FORTH-ICS
// Copyright 2015 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package hostpath contains the internal representation of hostPath volumes.
package hostpath

import (
	"context"
	"fmt"
	"os"

	"github.com/carv-ics-forth/hpk/compute/volume/util/validation"
	"github.com/carv-ics-forth/hpk/pkg/hostutil"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

// VolumeMounter handles retrieving secrets from the API server
// and placing them into the volume on the host.
type VolumeMounter struct {
	Volume corev1.Volume

	Pod corev1.Pod

	Logger logr.Logger
}

func (b *VolumeMounter) SetUpAt(ctx context.Context) error {
	source := b.Volume.HostPath

	if err := validation.ValidatePathNoBacksteps(source.Path); err != nil {
		return errors.Wrapf(err, "invalid HostPath '%s'", source.Path)
	}

	return checkType(source.Path, source.Type, hostutil.NewHostUtil())
}

type hostPathTypeChecker interface {
	Exists() bool
	IsFile() bool
	MakeFile() error
	IsDir() bool
	MakeDir() error
	IsBlock() bool
	IsChar() bool
	IsSocket() bool
	GetPath() string
}

// checkType checks whether the given path is the exact pathType
func checkType(path string, pathType *corev1.HostPathType, hu hostutil.HostUtils) error {
	return checkTypeInternal(newFileTypeChecker(path, hu), pathType)
}

func checkTypeInternal(ftc hostPathTypeChecker, pathType *corev1.HostPathType) error {
	switch *pathType {
	case corev1.HostPathDirectoryOrCreate:
		// In this mode, if no content is found in the specified path, an empty directory is created on demand.
		// The permission on the created directory is set to 0755. The directory has the same group
		// and ownership with kubelet.
		if !ftc.Exists() {
			return ftc.MakeDir()
		}
		fallthrough
	case corev1.HostPathDirectory:
		// A directory must exist in the specified path.
		if !ftc.IsDir() {
			return fmt.Errorf("hostPath type check failed: %s is not a directory", ftc.GetPath())
		}
	case corev1.HostPathFileOrCreate:
		// In this mode, if no content is found in the specified path, an empty file is created.
		// The permission of the created file is set to 0644. The file has the same group and ownership with kubelet.

		if !ftc.Exists() {
			return ftc.MakeFile()
		}
		fallthrough
	case corev1.HostPathFile:
		// A file must exist in the specified path.
		if !ftc.IsFile() {
			return fmt.Errorf("hostPath type check failed: %s is not a file", ftc.GetPath())
		}
	case corev1.HostPathSocket:
		// A UNIX socket must exist at the given path
		if !ftc.IsSocket() {
			return fmt.Errorf("hostPath type check failed: %s is not a socket file", ftc.GetPath())
		}
	case corev1.HostPathCharDev:
		// A character device must exist at the given path
		if !ftc.IsChar() {
			return fmt.Errorf("hostPath type check failed: %s is not a character device", ftc.GetPath())
		}
	case corev1.HostPathBlockDev:
		// A block device must exist at the given path

		if !ftc.IsBlock() {
			return fmt.Errorf("hostPath type check failed: %s is not a block device", ftc.GetPath())
		}
	default:
		return fmt.Errorf("%s is an invalid volume type", *pathType)
	}

	return nil
}

func newFileTypeChecker(path string, hu hostutil.HostUtils) hostPathTypeChecker {
	return &fileTypeChecker{path: path, hu: hu}
}

type fileTypeChecker struct {
	path string
	hu   hostutil.HostUtils
}

func (ftc *fileTypeChecker) Exists() bool {
	exists, err := ftc.hu.PathExists(ftc.path)
	return exists && err == nil
}

func (ftc *fileTypeChecker) IsFile() bool {
	if !ftc.Exists() {
		return false
	}
	pathType, err := ftc.hu.GetFileType(ftc.path)
	if err != nil {
		return false
	}
	return string(pathType) == string(corev1.HostPathFile)
}

func (ftc *fileTypeChecker) MakeFile() error {
	return makeFile(ftc.path)
}

func (ftc *fileTypeChecker) IsDir() bool {
	if !ftc.Exists() {
		return false
	}
	pathType, err := ftc.hu.GetFileType(ftc.path)
	if err != nil {
		return false
	}
	return string(pathType) == string(corev1.HostPathDirectory)
}

func (ftc *fileTypeChecker) MakeDir() error {
	return makeDir(ftc.path)
}

func (ftc *fileTypeChecker) IsBlock() bool {
	blkDevType, err := ftc.hu.GetFileType(ftc.path)
	if err != nil {
		return false
	}
	return string(blkDevType) == string(corev1.HostPathBlockDev)
}

func (ftc *fileTypeChecker) IsChar() bool {
	charDevType, err := ftc.hu.GetFileType(ftc.path)
	if err != nil {
		return false
	}
	return string(charDevType) == string(corev1.HostPathCharDev)
}

func (ftc *fileTypeChecker) IsSocket() bool {
	socketType, err := ftc.hu.GetFileType(ftc.path)
	if err != nil {
		return false
	}
	return string(socketType) == string(corev1.HostPathSocket)
}

func (ftc *fileTypeChecker) GetPath() string {
	return ftc.path
}

// makeDir creates a new directory.
// If pathname already exists as a directory, no error is returned.
// If pathname already exists as a file, an error is returned.
func makeDir(pathname string) error {
	err := os.MkdirAll(pathname, os.FileMode(0o755))
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// makeFile creates an empty file.
// If pathname already exists, whether a file or directory, no error is returned.
func makeFile(pathname string) error {
	f, err := os.OpenFile(pathname, os.O_CREATE, os.FileMode(0o644))
	if f != nil {
		f.Close()
	}
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}
