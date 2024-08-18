package main

import (
	"context"
	"fmt"
	"log"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MQTTClient struct {
	client   mqtt.Client
	broker   string
	clientID string
	ctx      context.Context
	topic    string
}

func NewMQTTClient(broker string, clientID string, topic string, ctx context.Context) *MQTTClient {
	// Create a new MQTT client
	opts := mqtt.NewClientOptions().AddBroker(broker).SetClientID(clientID)

	// Set up the connection lost handler
	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		fmt.Printf("Connection lost: %v\n", err)
	}

	// Set up the message handler
	opts.OnConnect = func(c mqtt.Client) {
		fmt.Println("Connected to broker")
	}

	// Create and start the MQTT client
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	return &MQTTClient{
		client:   client,
		broker:   broker,
		clientID: clientID,
		ctx:      ctx,
		topic:    topic,
	}

	// Wait for interrupt signal to gracefully shut down
	//sigChan := make(chan os.Signal, 1)
	//signal.Notify(sigChan, os.Interrupt)
	//<-sigChan
}

func (mc *MQTTClient) DisconnectMQTTClient() {
	// Clean up
	if token := mc.client.Unsubscribe(mc.topic); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	mc.client.Disconnect(250)
	fmt.Println("Client disconnected")
}

// publishMessages sends a message to the MQTT broker
func (mc *MQTTClient) PublishMessages(topic string, message string) {
	token := mc.client.Publish(topic, 0, false, message)
	token.Wait()

	if token.Error() != nil {
		// Handle the publish error
		fmt.Printf("Error publishing message: %v\n", token.Error())
	} else {
		fmt.Printf("Published message: %s\n", message)
	}
}

func (mc *MQTTClient) ReadMessages(messageHandler mqtt.MessageHandler) {
	fmt.Println("Subscribing to topic:", mc.topic)

	if token := mc.client.Subscribe(mc.topic, 0, messageHandler); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	<-mc.ctx.Done()
	mc.DisconnectMQTTClient()
}
