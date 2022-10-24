// Copyright © 2022 FORTH-ICS
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
// See the License for the specific language governing pe

package root

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"

	"github.com/carv-ics-forth/knoc/provider"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
)

// AcceptedCiphers is the list of accepted TLS ciphers, with known weak ciphers elided
// Note this list should be a moving target.
var AcceptedCiphers = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,

	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
}

func loadTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, errors.Wrap(err, "error loading tls certs")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: AcceptedCiphers,
	}, nil
}

type apiServerConfig struct {
	CertPath    string
	KeyPath     string
	Addr        string
	MetricsAddr string
}

func setupHTTPServer(ctx context.Context, p *provider.Provider, cfg *apiServerConfig) (_ func(), retErr error) {
	var closers []io.Closer

	cancel := func() {
		for _, c := range closers {
			c.Close()
		}
	}

	defer func() {
		if retErr != nil {
			cancel()
		}
	}()

	/*---------------------------------------------------
	 * Enable Logs and Interaction with container
	 *---------------------------------------------------*/
	{
		mux := http.NewServeMux()

		podRoutes := api.PodHandlerConfig{
			RunInContainer:   p.RunInContainer,
			GetContainerLogs: p.GetContainerLogs,
			GetPods:          p.GetPods,
		}

		api.AttachPodRoutes(podRoutes, mux, true)

		if cfg.CertPath == "" || cfg.KeyPath == "" {
			/*-- No TLS provided --*/
			s := &http.Server{
				Handler: mux,
				Addr:    cfg.Addr,
			}

			logrus.WithField("address", cfg.Addr).
				Warn("TLS certificates not provided. Setting up insecure pod http server.")

			go listenAndserveHTTP(ctx, s, "pods")

			closers = append(closers, s)
		} else {
			/*-- TLS provided --*/
			tlsCfg, err := loadTLSConfig(cfg.CertPath, cfg.KeyPath)
			if err != nil {
				return nil, err
			}

			l, err := tls.Listen("tcp", cfg.Addr, tlsCfg)
			if err != nil {
				return nil, errors.Wrap(err, "error setting up listener for pod http server")
			}

			s := &http.Server{
				Handler:   mux,
				TLSConfig: tlsCfg,
			}

			logrus.WithField("address", cfg.Addr).
				WithField("certPath", cfg.CertPath).
				WithField("keyPath", cfg.KeyPath).
				Warn("TLS certificates are found. Setting up secure pod http server.")

			go serveHTTP(ctx, s, l, "pods")

			closers = append(closers, s)
		}
	}

	/*---------------------------------------------------
	 * Enable Virtual-Kubelet Metrics
	 *---------------------------------------------------*/
	if cfg.MetricsAddr == "" {
		logrus.Warn("Pod metrics server not setup due to empty metrics address")
	} else {
		l, err := net.Listen("tcp", cfg.MetricsAddr)
		if err != nil {
			return nil, errors.Wrap(err, "could not setup listener for pod metrics http server")
		}

		mux := http.NewServeMux()

		var summaryHandlerFunc api.PodStatsSummaryHandlerFunc

		/*
			if mp, ok := p.(providers.PodMetricsProvider); ok {
				summaryHandlerFunc = mp.GetStatsSummary
			}

		*/

		podMetricsRoutes := api.PodMetricsConfig{
			GetStatsSummary: summaryHandlerFunc,
		}
		api.AttachPodMetricsRoutes(podMetricsRoutes, mux)
		s := &http.Server{
			Handler: mux,
		}

		go serveHTTP(ctx, s, l, "pod metrics")

		closers = append(closers, s)
	}

	return cancel, nil
}

func serveHTTP(ctx context.Context, s *http.Server, l net.Listener, name string) {
	if err := s.Serve(l); err != nil {
		select {
		case <-ctx.Done():
		default:
			log.G(ctx).WithError(err).Errorf("Error setting up %s http server", name)
		}
	}

	l.Close()
}

func listenAndserveHTTP(ctx context.Context, s *http.Server, name string) {
	if err := s.ListenAndServe(); err != nil {
		select {
		case <-ctx.Done():
		default:
			log.G(ctx).WithError(err).Errorf("Error setting up %s http server", name)
		}
	}
}
