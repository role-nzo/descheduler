package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Descheduler struct {
	clientset *kubernetes.Clientset
	ctx       context.Context
}

func NewDescheduler(clientset *kubernetes.Clientset, ctx context.Context) *Descheduler {
	return &Descheduler{
		clientset: clientset,
		ctx:       ctx,
	}
}

func (d *Descheduler) Run(descheduleIntervalFunction func(d *Descheduler)) {
	checkInterval := 30 * time.Second
	//N_tot, err := d.getTotalNodes()
	/*if err != nil {
		fmt.Printf("Error getting totNodes: %v\n", err)
		return
	}*/

	//fmt.Printf("Total nodes: %d\n", N_tot) //DEBUG

	for {
		time.Sleep(checkInterval)

		select {
		case <-d.ctx.Done():
			// Exit gracefully
			fmt.Println("Goroutine exiting due to context cancellation")
			return
		default:
			fmt.Printf("Descheduler running...\n") //DEBUG

			descheduleIntervalFunction(d)
		}
	}
}

func (d *Descheduler) DeschedulePod(podName string, namespace string) error {
	// Get the pod
	pod, err := d.clientset.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Check if the pod's deletion policy allows it to be deleted. If not, return an error.
	if pod.DeletionGracePeriodSeconds != nil && *pod.DeletionGracePeriodSeconds != 0 {
		return fmt.Errorf("pod %s has a deletion grace period of %d seconds", podName, *pod.DeletionGracePeriodSeconds)
	}

	// Delete the pod
	err = d.clientset.CoreV1().Pods(namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	fmt.Println("Successfully deleted pod", pod.Name)
	return nil
}

func (d *Descheduler) DescheduleAllPodsPerNode(appName string, nodeName string) error {
	// Get the list of pods on the worst performing node
	pods, err := d.clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})

	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		// Add any necessary filters here, e.g., by labels or namespace
		if strings.HasPrefix(pod.Namespace, "kube") || strings.HasPrefix(pod.Namespace, "local") || len(pod.Status.PodIP) == 0 { //scarto i pod di sistema o non ancora schedulati
			continue
		}
		currentAppName, ok := pod.Labels["app"]
		if !ok {
			continue
		}
		if currentAppName != appName {
			continue
		}
		// Check if the pod's deletion policy allows it to be deleted. If not, skip to the next pod.
		if pod.DeletionGracePeriodSeconds != nil && *pod.DeletionGracePeriodSeconds != 0 {
			continue
		}

		err = d.clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			// Log error and continue with next pod
			fmt.Println("Failed to delete pod", pod.Name, "with error", err.Error())
			continue
		}
		fmt.Println("Successfully deleted pod", pod.Name)
	}
	return nil
}

func (d *Descheduler) getTotalNodes() (int, error) {
	nodes, err := d.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return -1, err
	}
	return len(nodes.Items) - 1, nil
}
