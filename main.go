package main

import (
	"context"
	"encoding/json"
	"flag"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

var (
	masterURL  string
	kubeconfig string
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s.", err.Error())
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s.", err.Error())
	}

	// Create a Pod. Give a non-exist schedulerName to avoid being processed by default-scheduler.
	if err := CreatePod(cs); err != nil {
		klog.Fatalf("Cannot creat a Pod: %v", err)
	}
	pod, err := GetPod(cs, "default", "test")
	if err != nil {
		klog.Fatalf("Cannot get Pod: %v.", err)
	}

	// Update the Pod's NominatedNodeName to "minikube".
	nnn := "minikube"
	if err := UpdatePodStatus(cs, pod, nnn); err != nil {
		klog.Fatalf("Cannot update Pod: %v.", err)
	}
	// Modify the stale Pod in-place.
	pod.Status.NominatedNodeName = nnn

	// Clear the Pod's NominatedNodeName.
	// Using the stale Pod and operate with Update. A Conflict error is expected.
	if err := UpdatePodStatus(cs, pod, ""); err != nil {
		klog.Infof("UpdatePod with stale version: %v.", err)
	} else {
		klog.Fatalf("Expect error when updating Pod using a stale version, but got nil.")
	}
	// Still use the stale Pod, but operate with Patch.
	if err := PatchPodStatus(cs, pod, ""); err != nil {
		klog.Fatalf("PatchPod with stale version: %v.", err)
	}
	// Verify the Pod is patched properly.
	pod, err = GetPod(cs, "default", "test")
	if err != nil {
		klog.Fatalf("Cannot get Pod: %v.", err)
	}
	if nnn := pod.Status.NominatedNodeName; nnn != "" {
		klog.Fatalf("Expect empty pod.Status.NominatedNodeName, but got %v.", nnn)
	}

	// Bind the Pod to a non-exist Node.
	nodeName := "foo"
	if err := BindPod(cs, pod, nodeName); err != nil {
		klog.Fatalf("Cannot update Pod: %v", err)
	}
	// Try Patch the Pod using a stale version.
	// Note here we are passing in a mergepatch.PreconditionFunc to avoid Patch a Pod which
	// has its .spec.nodeName changed.
	if err := PatchPodStatus(cs, pod, nnn, RequireSpecKeyUnchanged("nodeName")); err != nil {
		klog.Infof("PatchPod with stale version: %v", err)
	} else {
		klog.Fatalf("Expect error when updating Pod using a stale version, but got nil.")
	}
}

func CreatePod(cs kubernetes.Interface) error {
	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "pause",
					Image: "k8s.gcr.io/pause:3.2",
				},
			},
			SchedulerName: "non-exist-sched",
		},
	}
	_, err := cs.CoreV1().Pods("default").Create(context.TODO(), &pod, metav1.CreateOptions{})
	return err
}

func UpdatePodStatus(cs kubernetes.Interface, pod *v1.Pod, nnn string) error {
	podCopy := pod.DeepCopy()
	podCopy.Status.NominatedNodeName = nnn
	_, err := cs.CoreV1().Pods(pod.Namespace).UpdateStatus(context.TODO(), podCopy, metav1.UpdateOptions{})
	return err
}

func PatchPodStatus(cs kubernetes.Interface, pod *v1.Pod, nnn string, fns ...mergepatch.PreconditionFunc) error {
	podCopy := pod.DeepCopy()
	oldData, err := json.Marshal(podCopy)
	if err != nil {
		return err
	}
	// if fns is not nil, fetch the live version.
	if len(fns) != 0 {
		livePod, err := GetPod(cs, pod.Namespace, pod.Name)
		if err != nil {
			return err
		}
		podCopy = livePod.DeepCopy()
	}
	podCopy.Status.NominatedNodeName = nnn
	newData, err := json.Marshal(podCopy)
	if err != nil {
		return err
	}

	patchData, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &v1.Pod{}, fns...)
	if err != nil {
		return err
	}

	_, err = cs.CoreV1().Pods(pod.Namespace).Patch(
		context.TODO(),
		pod.Name,
		types.StrategicMergePatchType,
		patchData,
		metav1.PatchOptions{},
		"status",
	)
	return err
}

func GetPod(cs kubernetes.Interface, ns, name string) (*v1.Pod, error) {
	return cs.CoreV1().Pods(ns).Get(context.TODO(), name, metav1.GetOptions{})
}

func BindPod(cs kubernetes.Interface, pod *v1.Pod, nodeName string) error {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}
	return cs.CoreV1().Pods(binding.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
}

func UpdatePod(cs kubernetes.Interface, pod *v1.Pod) error {
	podCopy := pod.DeepCopy()
	_, err := cs.CoreV1().Pods(pod.Namespace).Update(context.TODO(), podCopy, metav1.UpdateOptions{})
	return err
}

func RequireSpecKeyUnchanged(key string) mergepatch.PreconditionFunc {
	return func(patch interface{}) bool {
		patchMap, ok := patch.(map[string]interface{})
		if !ok {
			return true
		}
		patchMap1, ok := patchMap["spec"]
		if !ok {
			return true
		}
		patchMap2, ok := patchMap1.(map[string]interface{})
		if !ok {
			return true
		}
		_, ok = patchMap2[key]
		return !ok
	}
}
