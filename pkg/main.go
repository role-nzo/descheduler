package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type Measurement struct {
	// The name of the pod
	PodName string
	// The name of the pod
	Namespace string
	// The latency measurement
	RTT time.Duration
	// The time at which the measurement was taken
	Timestamp time.Time
}

func main() {
	fmt.Println("Starting custom descheduler...")

	// Parametro per il file di configurazione kubeconfig
	kubeconfig := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	// Parametro per l'URL del broker MQTT
	broker := flag.String("broker", "tcp://broker.hivemq.com:1883", "MQTT broker URL")
	// Parametro per il topic MQTT
	topic := flag.String("topic", "test/topic123679", "MQTT topic")
	// Parametro per l'ID del client MQTT
	clientID := flag.String("clientID", "clientId-KI02s7qxUZ", "MQTT client ID")
	// Parametro per l'ID del client MQTT
	measurementsCountArg := flag.String("measurements", "3", "Number of measurements to take before descheduling")

	flag.Parse()

	// Converti il numero di misurazioni in un intero
	measurementsCount, err := strconv.Atoi(*measurementsCountArg)
	if err != nil {
		fmt.Println("Error converting port to integer:", err)
		return
	}

	if *kubeconfig == "" {
		fmt.Println("kubeconfig path must be specified")
		return
	}

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		fmt.Println("Error building config:", err)
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println("Error creating clientset:", err)
		return
	}

	var measurements = make([]Measurement, 0)

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())

	descheduler := NewDescheduler(clientset, ctx)
	mqttclient := NewMQTTClient(*broker, *clientID, *topic, ctx)

	wg.Add(1)
	go func() {
		descheduler.Run(func(d *Descheduler) {
			if len(measurements) >= measurementsCount {
				// get the measurement with the biggest RTT
				// Initialize the index of the measurement with the biggest RTT
				maxRTTIndex := 0

				// Iterate over the measurements slice
				for i := 1; i < len(measurements); i++ {
					// Check if the current measurement has a bigger RTT than the measurement with the biggest RTT
					if measurements[i].RTT > measurements[maxRTTIndex].RTT {
						// Update the index of the measurement with the biggest RTT
						maxRTTIndex = i
					} else if measurements[i].RTT == measurements[maxRTTIndex].RTT {
						// If the RTTs are equal, check the timestamps: the oldest measurement should be descheduled
						if measurements[i].Timestamp.Before(measurements[maxRTTIndex].Timestamp) {
							// Update the index of the measurement with the biggest RTT
							maxRTTIndex = i
						}
					}
				}

				// Get the measurement with the biggest RTT
				maxRTTMeasurement := measurements[maxRTTIndex]

				fmt.Println("Descheduling pod:", maxRTTMeasurement.PodName)

				// Deschedule the podname
				err := d.DeschedulePod(maxRTTMeasurement.PodName, maxRTTMeasurement.Namespace)
				if err != nil {
					fmt.Println("Error descheduling pod:", err)
				}

				// Delete the entry from measurements (even in case of error)
				measurements = append(measurements[:maxRTTIndex], measurements[maxRTTIndex+1:]...)
			}
		})
	}()

	wg.Add(1)
	go func() {
		mqttclient.ReadMessages(func(_ mqtt.Client, message mqtt.Message) {
			fmt.Printf("Received message: %s on topic: %s\n", message.Payload(), message.Topic())

			// Parse the message
			// The message should be in the format: "Pod: %s\Namespace: %s\nRTT: %s\nTimestamp: %s"
			messageStr := string(message.Payload())
			var podName, namespace, rtt, timestamp string
			_, err := fmt.Sscanf(messageStr, "Pod: %s\nNamespace: %s\nRTT: %s\nTimestamp: %s", &podName, &namespace, &rtt, &timestamp)
			if err != nil {
				fmt.Println("Error parsing message:", err)
				return
			}

			//convert rtt into time duration (format is 1.234567ms)
			rttDuration, err := time.ParseDuration(rtt)
			if err != nil {
				fmt.Println("Error parsing rtt:", err)
				return
			}

			// convert timestamp into time.Time (format is 2024-08-18T14:08:11Z)
			timestampTime, err := time.Parse(time.RFC3339, timestamp)
			if err != nil {
				fmt.Println("Error parsing timestamp:", err)
				return
			}

			// Create a new measurement
			measurement := Measurement{
				PodName:   podName,
				Namespace: namespace,
				RTT:       rttDuration,
				Timestamp: timestampTime,
			}

			// Check if measurements already contains an entry with the same podname
			for i := 0; i < len(measurements); i++ {
				if measurements[i].PodName == podName {
					// Replace the existing entry with the new measurement
					measurements[i] = measurement
					return
				}
			}

			// Append the measurement to the measurements slice
			measurements = append(measurements, measurement)

			// Total measurements
			fmt.Println("Total measurements:", len(measurements))
		})
	}()

	// Wait for interrupt signal to gracefully shut down
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	<-sigChan

	cancel()

	wg.Wait() // Attendi che entrambe le goroutine siano terminate
}
