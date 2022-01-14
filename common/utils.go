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
// limitations under the License.package common

package common

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/pkg/sftp"
	"github.com/sfreiberg/simplessh"
	v1 "k8s.io/api/core/v1"
)

func UploadData(client *simplessh.Client, data []byte, remote string, mode fs.FileMode) error {
	c, err := sftp.NewClient(client.SSHClient)
	if err != nil {
		fmt.Println("Could not connect over sftp on the remote system ")
		return err
	}
	defer c.Close()

	remoteFile, err := c.Create(remote)
	if err != nil {
		fmt.Println("Could not create file over sftp on the remote system ")
		return err
	}

	_, err = remoteFile.Write(data)

	if err != nil {
		fmt.Println("Could not write content on the remote system ")
		return err
	}
	err = c.Chmod(remote, mode)
	if err != nil {
		return err
	}
	return nil
}

func UploadFile(client *simplessh.Client, local string, remote string, mode fs.FileMode) error {
	c, err := sftp.NewClient(client.SSHClient)
	if err != nil {
		fmt.Println("Could not connect over sftp on the remote system ")
		return err
	}
	defer c.Close()

	localFile, err := os.Open(local)
	if err != nil {
		fmt.Println("Could not open local file in path: " + local)
		return err
	}
	defer localFile.Close()

	remoteFile, err := c.Create(remote)
	if err != nil {
		fmt.Println("Could not create file over sftp on the remote system ")
		return err
	}

	_, err = io.Copy(remoteFile, localFile)
	if err != nil {
		fmt.Println("Could not copy file on the remote system: ")
		return err
	}
	err = c.Chmod(remote, mode)
	if err != nil {
		return err
	}
	return nil
}

func NormalizeImageName(instance_name string) string {
	instances_str := strings.Split(string(instance_name), "/")
	final_name := ""
	first_iter := true
	for _, strings := range instances_str {
		if first_iter {
			final_name = strings
			first_iter = false
			continue
		}
		final_name = final_name + "-" + strings
	}
	without_version_stamp := strings.Split(final_name, ":")
	return without_version_stamp[0]
}

func BuildKeyFromNames(namespace string, name string) (string, error) {
	return fmt.Sprintf("%s-%s", namespace, name), nil
}

// buildKey is a helper for building the "key" for the providers pod store.
func BuildKey(pod *v1.Pod) (string, error) {
	if pod.ObjectMeta.Namespace == "" {
		return "", fmt.Errorf("pod namespace not found")
	}

	if pod.ObjectMeta.Name == "" {
		return "", fmt.Errorf("pod name not found")
	}

	return BuildKeyFromNames(pod.ObjectMeta.Namespace, pod.ObjectMeta.Name)
}
