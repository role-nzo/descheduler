package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Descheduler struct {
	clientset      *kubernetes.Clientset
	ctx            context.Context
	targetAppLabel string
	probeAppLabel  string
	labelSelector  string
}

func NewDescheduler(clientset *kubernetes.Clientset, ctx context.Context, targetAppLabel string, probeAppLabel string, labelSelector string) *Descheduler {
	return &Descheduler{
		clientset:      clientset,
		ctx:            ctx,
		targetAppLabel: targetAppLabel,
		probeAppLabel:  probeAppLabel,
		labelSelector:  labelSelector,
	}
}

func (d *Descheduler) Run(descheduleIntervalFunction func(d *Descheduler), podDescheduledFunction func(d *Descheduler, pod *corev1.Pod)) {
	checkInterval := 30 * time.Second

	// It is used a informer strategy because in some cases the scheduler itself may deschedule pods (e.g., when a target pod substitutes a probe pod)
	//	in this way the descheduler can be notified of the descheduled pods and act accordingly (delete the corresponding measurement)

	// Create a shared informer factory
	sharedInformerFactory := informers.NewSharedInformerFactory(d.clientset, 0)

	// Get the pod informer
	podInformer := sharedInformerFactory.Core().V1().Pods().Informer()

	// Add event handlers for pod creation and deletion
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			podDescheduledFunction(d, pod)
		},
	})

	// Start the informer
	go sharedInformerFactory.Start(d.ctx.Done())

	// Wait for the informer cache to sync
	if !cache.WaitForCacheSync(d.ctx.Done(), podInformer.HasSynced) {
		fmt.Println("[Descheduler] Error syncing informer cache")
		return
	}

	fmt.Println("[Descheduler] Informer started...")

	for {
		time.Sleep(checkInterval)

		select {
		case <-d.ctx.Done():
			// Exit gracefully
			fmt.Println("[Descheduler] Goroutine exiting due to context cancellation")
			return
		default:
			fmt.Printf("[Descheduler] Descheduler running...\n") //DEBUG

			descheduleIntervalFunction(d)
		}
	}
}

func (d *Descheduler) IsStable() bool {
	// The deployment is stable iff: the number of target pods running is equal to the number of replicas & the number of probe pods running is zero

	podList, err := d.getPodsByLabelSelector(d.ctx, d.labelSelector)
	if err != nil {
		fmt.Printf("[Descheduler] Error listing pods: %v", err)
		return true
	}

	// Count the number of target pods running
	for _, pod := range podList.Items {
		// If NodeName is not empty (the pod is scheduled)
		appLabel := pod.Labels["app"]

		if appLabel == d.targetAppLabel {
			// if the target pod is not in pending or running state -> the target needs to be scheduled
			if pod.DeletionTimestamp != nil || (pod.Status.Phase != corev1.PodPending && pod.Status.Phase != corev1.PodRunning) {
				return false
			}
		} else if appLabel == d.probeAppLabel {
			// if the probe pod is in pending or running state and not in deletion -> the probe needs to be descheduled
			if pod.DeletionTimestamp == nil && (pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning) {
				return false
			}
		}
	}

	return true
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

	fmt.Println("[Descheduler] Successfully deleted pod", pod.Name)
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
			fmt.Println("[Descheduler] Failed to delete pod", pod.Name, "with error", err.Error())
			continue
		}
		fmt.Println("[Descheduler] Successfully deleted pod", pod.Name)
	}
	return nil
}

func (d *Descheduler) getPodsByLabelSelector(ctx context.Context, labelSelector string) (*corev1.PodList, error) {
	podList, err := d.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	return podList, nil
}
