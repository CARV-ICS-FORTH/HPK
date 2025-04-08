// Copyright © 2022 Antony Chazapis
// Copyright © 2018 Marton Sereg
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

package main

import (
    "os"
    "path/filepath"
    "fmt"
    "log"
    "math/rand"
    "time"
    "context"

    "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/labels"
    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
    listersv1 "k8s.io/client-go/listers/core/v1"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/client-go/tools/cache"
    "k8s.io/apimachinery/pkg/util/wait"
)

const schedulerName = "random-scheduler"

type Scheduler struct {
    clientset  *kubernetes.Clientset
    podQueue   chan *v1.Pod
    nodeLister listersv1.NodeLister
}

func initInformers(clientset *kubernetes.Clientset, podQueue chan *v1.Pod, quit chan struct{}) listersv1.NodeLister {
    factory := informers.NewSharedInformerFactory(clientset, 0)

    nodeInformer := factory.Core().V1().Nodes()
    nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            node, ok := obj.(*v1.Node)
            if !ok {
                log.Println("This is not a node")
                return
            }
            log.Printf("New node added to store: %s\n", node.GetName())
        },
    })

    podInformer := factory.Core().V1().Pods()
    podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            pod, ok := obj.(*v1.Pod)
            if !ok {
                log.Println("This is not a pod")
                return
            }
            if pod.Spec.NodeName == "" {
                podQueue <- pod
            }
        },
    })

    factory.Start(quit)
    return nodeInformer.Lister()
}

func newScheduler(podQueue chan *v1.Pod, quit chan struct{}) Scheduler {
    kubeconfig := os.Getenv("KUBECONFIG")
    if kubeconfig == "" {
        home, _ := os.UserHomeDir()
        kubeconfig = filepath.Join(home, ".kube", "config")
    }
    config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
    if err != nil {
        config, err = rest.InClusterConfig()
    }
    if err != nil {
        log.Fatal(err)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Fatal(err)
    }

    return Scheduler{
        clientset:  clientset,
        podQueue:   podQueue,
        nodeLister: initInformers(clientset, podQueue, quit),
    }
}

func (s *Scheduler) run(quit chan struct{}) {
    wait.Until(s.scheduleOne, 0, quit)
}

func (s *Scheduler) scheduleOne() {
    p := <-s.podQueue
    fmt.Printf("Found a pod to schedule: %s/%s\n", p.Namespace, p.Name)

    // Select a random node
    nodes, err := s.nodeLister.List(labels.Everything())
    if err != nil {
        log.Println("Failed to get nodes", err.Error())
        return
    }
    node := nodes[rand.Intn(len(nodes))]

    // Bind pod
    err = s.bindPod(p, node.Name)
    if err != nil {
        log.Println("Failed to bind pod", err.Error())
        return
    }
    message := fmt.Sprintf("Placed pod %s/%s on %s\n", p.Namespace, p.Name, node.Name)

    // Schedule
    err = s.emitEvent(p, message)
    if err != nil {
        log.Println("Failed to emit scheduled event", err.Error())
        return
    }

    fmt.Print(message)
}

func (s *Scheduler) bindPod(p *v1.Pod, node string) error {
    return s.clientset.CoreV1().Pods(p.Namespace).Bind(context.Background(), &v1.Binding{
        ObjectMeta: metav1.ObjectMeta{
            Name:      p.Name,
            Namespace: p.Namespace,
        },
        Target: v1.ObjectReference{
            APIVersion: "v1",
            Kind:       "Node",
            Name:       node,
        },
    }, metav1.CreateOptions{})
}

func (s *Scheduler) emitEvent(p *v1.Pod, message string) error {
    timestamp := time.Now().UTC()
    _, err := s.clientset.CoreV1().Events(p.Namespace).Create(context.Background(), &v1.Event{
        Count:          1,
        Message:        message,
        Reason:         "Scheduled",
        LastTimestamp:  metav1.NewTime(timestamp),
        FirstTimestamp: metav1.NewTime(timestamp),
        Type:           "Normal",
        Source: v1.EventSource{
            Component: schedulerName,
        },
        InvolvedObject: v1.ObjectReference{
            Kind:      "Pod",
            Name:      p.Name,
            Namespace: p.Namespace,
            UID:       p.UID,
        },
        ObjectMeta: metav1.ObjectMeta{
            GenerateName: p.Name + "-",
        },
    }, metav1.CreateOptions{})
    if err != nil {
        return err
    }
    return nil
}

func main() {
    rand.Seed(time.Now().Unix())

    podQueue := make(chan *v1.Pod, 300)
    defer close(podQueue)

    quit := make(chan struct{})
    defer close(quit)

    scheduler := newScheduler(podQueue, quit)
    scheduler.run(quit)
}
