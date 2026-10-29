package main

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
)

func TestMQTTLiveIDAndRetainedIgnored(t *testing.T) {
	a := apiApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	defer cancel()
	listener := listenTest(t)
	a.cfg.MQTT.Enabled = true
	a.cfg.MQTT.Broker = "tcp://" + listener.Addr().String()
	client := a.startMQTT()
	defer client.Disconnect(0)
	result := make(chan error, 1)
	sendLive := make(chan struct{})
	retainedSent := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		p, err := packets.ReadPacket(conn)
		if err != nil {
			result <- err
			return
		}
		connect, ok := p.(*packets.ConnectPacket)
		if !ok || !connect.CleanSession {
			result <- fmt.Errorf("expected clean MQTT session")
			return
		}
		ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
		if err = ack.Write(conn); err != nil {
			result <- err
			return
		}
		p, err = packets.ReadPacket(conn)
		if err != nil {
			result <- err
			return
		}
		sub, ok := p.(*packets.SubscribePacket)
		if !ok || len(sub.Topics) != 1 || sub.Topics[0] != a.cfg.MQTT.Topic || sub.Qoss[0] != 0 {
			result <- fmt.Errorf("expected QoS 0 subscription")
			return
		}
		suback := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
		suback.MessageID = sub.MessageID
		suback.ReturnCodes = []byte{0}
		suback.Write(conn)
		publish := func(retain bool) error {
			p := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
			p.Retain = retain
			p.TopicName = a.cfg.MQTT.Topic
			p.Payload = []byte("gong")
			return p.Write(conn)
		}
		if err = publish(true); err != nil {
			result <- err
			return
		}
		close(retainedSent)
		select {
		case <-sendLive:
		case <-ctx.Done():
			result <- ctx.Err()
			return
		}
		if err = publish(false); err != nil {
			result <- err
			return
		}
		result <- nil
		io.Copy(io.Discard, conn)
	}()
	select {
	case <-retainedSent:
	case err := <-result:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("MQTT subscribe timed out")
	}
	select {
	case <-a.jobs:
		t.Fatal("retained message triggered playback")
	case <-time.After(100 * time.Millisecond):
	}
	close(sendLive)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MQTT publish timed out")
	}
	select {
	case job := <-a.jobs:
		if job.Label != "gong" {
			t.Fatal("wrong MQTT sound")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("live message was not queued")
	}
}
