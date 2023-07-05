package kor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a todo",
	Long:  `This command will create todo`,
}

func retrieveVolumesAndEnv(clientset *kubernetes.Clientset, namespace string) ([]string, []string, []string, []string, []string, error) {
	volumesCM := []string{}
	volumesProjectedCM := []string{}
	envCM := []string{}
	envFromCM := []string{}
	envFromContainerCM := []string{}

	// Retrieve pods in the specified namespace
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// Extract volume and environment information from pods
	for _, pod := range pods.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.ConfigMap != nil {
				volumesCM = append(volumesCM, volume.ConfigMap.Name)
			}
			if volume.Projected != nil {
				for _, source := range volume.Projected.Sources {
					if source.ConfigMap != nil {
						volumesProjectedCM = append(volumesProjectedCM, source.ConfigMap.Name)
					}
				}
			}
		}
		for _, container := range pod.Spec.Containers {
			for _, env := range container.Env {
				if env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil {
					envCM = append(envCM, env.ValueFrom.ConfigMapKeyRef.Name)
				}
			}
			for _, envFrom := range container.EnvFrom {
				if envFrom.ConfigMapRef != nil {
					envFromCM = append(envFromCM, envFrom.ConfigMapRef.Name)
				}
			}
			for _, envFrom := range container.EnvFrom {
				if envFrom.ConfigMapRef != nil {
					envFromContainerCM = append(envFromContainerCM, envFrom.ConfigMapRef.Name)
				}
			}
		}
	}

	return volumesCM, volumesProjectedCM, envCM, envFromCM, envFromContainerCM, nil
}

func retrieveConfigMapNames(clientset *kubernetes.Clientset, namespace string) ([]string, error) {
	configmaps, err := clientset.CoreV1().ConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(configmaps.Items))
	for _, configmap := range configmaps.Items {
		names = append(names, configmap.Name)
	}
	return names, nil
}

func calculateDifference(usedConfigMaps []string, configMapNames []string) []string {
	difference := []string{}
	for _, name := range configMapNames {
		found := false
		for _, usedName := range usedConfigMaps {
			if name == usedName {
				found = true
				break
			}
		}
		if !found {
			difference = append(difference, name)
		}
	}
	return difference
}

func formatOutput(namespace string, configMapNames []string) string {
	if len(configMapNames) == 0 {
		return fmt.Sprintf("No unused config maps found in the namespace: %s", namespace)
	}

	var buf bytes.Buffer
	table := tablewriter.NewWriter(&buf)
	table.SetHeader([]string{"#", "Config Map Name"})

	for i, name := range configMapNames {
		table.Append([]string{fmt.Sprintf("%d", i+1), name})
	}

	table.Render()

	return fmt.Sprintf("Unused Config Maps in Namespace: %s\n%s", namespace, buf.String())
}

func processNamespace(clientset *kubernetes.Clientset, namespace string) (string, error) {
	volumesCM, volumesProjectedCM, envCM, envFromCM, envFromContainerCM, err := retrieveVolumesAndEnv(clientset, namespace)
	if err != nil {
		return "", err
	}

	volumesCM = removeDuplicatesAndSort(volumesCM)
	volumesProjectedCM = removeDuplicatesAndSort(volumesProjectedCM)
	envCM = removeDuplicatesAndSort(envCM)
	envFromCM = removeDuplicatesAndSort(envFromCM)
	envFromContainerCM = removeDuplicatesAndSort(envFromContainerCM)

	configMapNames, err := retrieveConfigMapNames(clientset, namespace)
	if err != nil {
		return "", err
	}

	usedConfigMaps := append(append(append(append(volumesCM, volumesProjectedCM...), envCM...), envFromCM...), envFromContainerCM...)
	diff := calculateDifference(usedConfigMaps, configMapNames)
	return formatOutput(namespace, diff), nil

}

func removeDuplicatesAndSort(slice []string) []string {
	uniqueSet := make(map[string]bool)
	for _, item := range slice {
		uniqueSet[item] = true
	}
	uniqueSlice := make([]string, 0, len(uniqueSet))
	for item := range uniqueSet {
		uniqueSlice = append(uniqueSlice, item)
	}
	sort.Strings(uniqueSlice)
	return uniqueSlice
}

func GetUnusedConfigmaps() {
	var kubeconfig string
	var namespaces []string

	kubeconfig = getKubeConfigPath()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load kubeconfig: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	namespaceList, err := clientset.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to retrieve namespaces: %v\n", err)
		os.Exit(1)
	}
	for _, ns := range namespaceList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	for _, namespace := range namespaces {
		output, err := processNamespace(clientset, namespace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to process namespace %s: %v\n", namespace, err)
			continue
		}
		fmt.Println(output)
		fmt.Println()
	}
}

func getKubeConfigPath() string {
	home := homedir.HomeDir()
	return filepath.Join(home, ".kube", "config")
}

func excludeListContains(excludeList []string, namespace string) bool {
	for _, excluded := range excludeList {
		if excluded == namespace {
			return true
		}
	}
	return false
}
