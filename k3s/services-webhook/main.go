// Copyright © 2022 Antony Chazapis
// Copyright © 2018 Morven Kao
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
    "context"
    "crypto/tls"
    "flag"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
)

var (
    infoLogger      *log.Logger
    warningLogger   *log.Logger
    errorLogger     *log.Logger
)

var (
    port            int
)

func init() {
    // init loggers
    infoLogger = log.New(os.Stderr, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
    warningLogger = log.New(os.Stderr, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
    errorLogger = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}

func main() {
    var port        int    // webhook server port
    var certFile    string // path to the x509 certificate for https
    var keyFile     string // path to the x509 private key matching `certFile`

    // Init command flags
    flag.IntVar(&port, "port", 8443, "Webhook server port.")
    flag.StringVar(&certFile, "tlsCertFile", "/etc/webhook/certs/cert.pem", "x509 certificate file.")
    flag.StringVar(&keyFile, "tlsKeyFile", "/etc/webhook/certs/key.pem", "x509 private key file.")
    flag.Parse()

    pair, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        errorLogger.Fatalf("Failed to load key pair: %v", err)
    }

    whsvr := &WebhookServer{
        server: &http.Server{
            Addr:      fmt.Sprintf(":%v", port),
            TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}},
        },
    }

    // Define HTTP server and server handler
    mux := http.NewServeMux()
    mux.HandleFunc("/mutate", whsvr.serve)
    whsvr.server.Handler = mux

    // Start webhook server in new rountine
    go func() {
        if err := whsvr.server.ListenAndServeTLS("", ""); err != nil {
            errorLogger.Fatalf("Failed to listen and serve webhook server: %v", err)
        }
    }()

    // Listen for shutdown singal
    signalChan := make(chan os.Signal, 1)
    signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
    <-signalChan

    infoLogger.Printf("Got shutdown signal, shutting down webhook server gracefully...")
    whsvr.server.Shutdown(context.Background())
}
