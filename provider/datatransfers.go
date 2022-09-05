// Copyright © 2021 FORTH-ICS
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
// limitations under the License.package main

package provider

import (
	"io"
	"io/fs"
	"os"

	"github.com/pkg/errors"
)

func UploadData(data []byte, remote string, mode fs.FileMode) error {
	c, err := SSHClient.SFTPClient()
	if err != nil {
		return errors.Wrap(err, "Could not connect over sftp on the remote system ")
	}
	defer c.Close()

	remoteFile, err := c.Create(remote)
	if err != nil {
		return errors.Wrapf(err, "Could not create file over sftp on the remote system ")
	}

	if _, err := remoteFile.Write(data); err != nil {
		return errors.Wrapf(err, "Could not write content on the remote system ")
	}

	if err := c.Chmod(remote, mode); err != nil {
		return errors.Wrapf(err, "chmod")
	}

	return nil
}

func UploadFile(local string, remote string, mode fs.FileMode) error {
	c, err := SSHClient.SFTPClient()
	if err != nil {
		return errors.Wrap(err, "Could not connect over sftp on the remote system ")
	}
	defer c.Close()

	localFile, err := os.Open(local)
	if err != nil {
		return errors.Wrapf(err, "Could not open local file in path: %s", local)
	}
	defer localFile.Close()

	remoteFile, err := c.Create(remote)
	if err != nil {
		return errors.Wrapf(err, "Could not create file over sftp on the remote system ")
	}

	if _, err = io.Copy(remoteFile, localFile); err != nil {
		return errors.Wrapf(err, "Could not copy file on the remote system: ")
	}

	if err := c.Chmod(remote, mode); err != nil {
		return errors.Wrapf(err, "chmod")
	}

	return nil
}
