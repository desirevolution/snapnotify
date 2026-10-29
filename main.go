package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var version = "dev"

func (a *App) startMQTT() mqtt.Client {
	if !a.cfg.MQTT.Enabled {
		return nil
	}
	inbox := make(chan string, a.cfg.Limits.QueueSize)
	setState := func(s string) { a.mu.Lock(); a.status.MQTT = s; a.mu.Unlock() }
	opts := mqtt.NewClientOptions().AddBroker(a.cfg.MQTT.Broker).SetClientID(a.cfg.MQTT.ClientID).SetUsername(a.cfg.MQTT.Username).SetPassword(a.cfg.MQTT.Password).SetCleanSession(true).SetResumeSubs(false).SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(5 * time.Second).SetConnectTimeout(5 * time.Second).SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, _ error) { setState("disconnected"); slog.Warn("MQTT disconnected") })
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		if a.ctx.Err() != nil {
			return
		}
		t := c.Subscribe(a.cfg.MQTT.Topic, 0, func(_ mqtt.Client, m mqtt.Message) {
			if m.Retained() {
				return
			}
			if !validID.Match(m.Payload()) {
				slog.Warn("MQTT message rejected: expected sound ID")
				return
			}
			select {
			case inbox <- string(m.Payload()):
			default:
				slog.Warn("MQTT input queue full; message discarded")
			}
		})
		if !t.WaitTimeout(5*time.Second) || t.Error() != nil {
			setState("subscription failed")
			slog.Error("MQTT subscription failed")
			return
		}
		setState("connected")
	})
	c := mqtt.NewClient(opts)
	go func() { c.Connect() }()
	go func() {
		for {
			select {
			case <-a.ctx.Done():
				return
			case id := <-inbox:
				if a.ctx.Err() != nil {
					return
				}
				if err := a.acquire(); err != nil {
					a.event(Job{ID: newID(), Label: id}, "failed", err)
					continue
				}
				job, err := a.local(a.ctx, id)
				if err == nil {
					err = a.enqueue(job)
				}
				a.release()
				if err != nil {
					if job.Path != "" {
						os.Remove(job.Path)
					}
					a.event(Job{ID: newID(), Label: id}, "failed", err)
				}
			}
		}
	}()
	return c
}

func main() {
	if err := runMain(); err != nil {
		slog.Error("snapnotify stopped", "error", err)
		os.Exit(1)
	}
}
func runMain() error {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	showVersion := flag.Bool("version", false, "print version")
	healthcheck := flag.String("healthcheck", "", "check service health URL and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if *healthcheck != "" {
		client := http.Client{Timeout: 2 * time.Second}
		r, err := client.Get(*healthcheck)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return fmt.Errorf("unhealthy: HTTP %d", r.StatusCode)
		}
		return nil
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if p, ok := os.LookupEnv("SNAPNOTIFY_MPD_PASSWORD"); ok {
		cfg.MPD.Password = p
	}
	if p, ok := os.LookupEnv("SNAPNOTIFY_MQTT_PASSWORD"); ok {
		cfg.MQTT.Password = p
	}
	tempDir, err := os.MkdirTemp("", "snapnotify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, cfg, tempDir)
	listener, err := net.Listen("tcp", cfg.HTTP.Listen)
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go a.run(done)
	mqttClient := a.startMQTT()
	server := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: time.Duration(cfg.Limits.DownloadTimeoutSeconds+60) * time.Second, WriteTimeout: time.Duration(cfg.Limits.DownloadTimeoutSeconds+120) * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	errors := make(chan error, 1)
	go func() { errors <- server.Serve(listener) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	slog.Info("snapnotify listening", "address", listener.Addr(), "version", version)
	select {
	case <-signals:
	case err = <-errors:
		if err == http.ErrServerClosed {
			err = nil
		}
	}
	a.mu.Lock()
	a.closing = true
	a.mu.Unlock()
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if e := server.Shutdown(shutdownCtx); e != nil {
		server.Close()
	}
	if mqttClient != nil {
		mqttClient.Disconnect(0)
	}
	a.prepareWG.Wait()
	<-done
	return err
}
