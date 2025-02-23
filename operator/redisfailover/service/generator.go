package service

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	redisfailoverv1 "github.com/spotahome/redis-operator/api/redisfailover/v1"
	"github.com/spotahome/redis-operator/operator/redisfailover/util"
)

const (
	redisConfigurationVolumeName         = "redis-config"
	redisShutdownConfigurationVolumeName = "redis-shutdown-config"
	redisStorageVolumeName               = "redis-data"

	graceTime = 30
)

func generateSentinelService(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.Service {
	name := GetSentinelName(rf)
	namespace := rf.Namespace

	sentinelTargetPort := intstr.FromInt(26379)
	selectorLabels := generateSelectorLabels(sentinelRoleName, rf.Name)
	labels = util.MergeLabels(labels, selectorLabels)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "sentinel",
					Port:       26379,
					TargetPort: sentinelTargetPort,
					Protocol:   "TCP",
				},
			},
		},
	}
}

func generateRedisService(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.Service {
	name := GetRedisName(rf)
	namespace := rf.Namespace

	selectorLabels := generateSelectorLabels(redisRoleName, rf.Name)
	labels = util.MergeLabels(labels, selectorLabels)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "http",
				"prometheus.io/path":   "/metrics",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
			Ports: []corev1.ServicePort{
				{
					Port:     exporterPort,
					Protocol: corev1.ProtocolTCP,
					Name:     exporterPortName,
				},
			},
			Selector: selectorLabels,
		},
	}
}

func generateSentinelConfigMap(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.ConfigMap {
	name := GetSentinelName(rf)
	namespace := rf.Namespace

	labels = util.MergeLabels(labels, generateSelectorLabels(sentinelRoleName, rf.Name))
	sentinelConfigFileContent := `sentinel monitor mymaster 127.0.0.1 6379 2
sentinel down-after-milliseconds mymaster 1000
sentinel failover-timeout mymaster 3000
sentinel parallel-syncs mymaster 2`

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Data: map[string]string{
			sentinelConfigFileName: sentinelConfigFileContent,
		},
	}
}

func generateRedisConfigMap(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference, password string) *corev1.ConfigMap {
	name := GetRedisName(rf)
	namespace := rf.Namespace

	labels = util.MergeLabels(labels, generateSelectorLabels(redisRoleName, rf.Name))
	redisConfigFileContent := `slaveof 127.0.0.1 6379
tcp-keepalive 60
save 900 1
save 300 10`

	if password != "" {
		redisConfigFileContent = fmt.Sprintf("%s\nmasterauth %s\nrequirepass %s", redisConfigFileContent, password, password)
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Data: map[string]string{
			redisConfigFileName: redisConfigFileContent,
		},
	}
}

func generateRedisShutdownConfigMap(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.ConfigMap {
	name := GetRedisShutdownConfigMapName(rf)
	namespace := rf.Namespace

	labels = util.MergeLabels(labels, generateSelectorLabels(redisRoleName, rf.Name))
	shutdownContent := `master=$(redis-cli -h ${RFS_REDIS_SERVICE_HOST} -p ${RFS_REDIS_SERVICE_PORT_SENTINEL} --csv SENTINEL get-master-addr-by-name mymaster | tr ',' ' ' | tr -d '\"' |cut -d' ' -f1)
redis-cli SAVE
if [[ $master ==  $(hostname -i) ]]; then
  redis-cli -h ${RFS_REDIS_SERVICE_HOST} -p ${RFS_REDIS_SERVICE_PORT_SENTINEL} SENTINEL failover mymaster
fi`

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Data: map[string]string{
			"shutdown.sh": shutdownContent,
		},
	}
}

func generateRedisStatefulSet(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference) *appsv1.StatefulSet {
	name := GetRedisName(rf)
	namespace := rf.Namespace

	redisCommand := getRedisCommand(rf)
	selectorLabels := generateSelectorLabels(redisRoleName, rf.Name)
	labels = util.MergeLabels(labels, selectorLabels)
	volumeMounts := getRedisVolumeMounts(rf)
	volumes := getRedisVolumes(rf)

	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    &rf.Spec.Redis.Replicas,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: "OnDelete",
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: rf.Spec.Redis.PodAnnotations,
				},
				Spec: corev1.PodSpec{
					Affinity:        getAffinity(rf.Spec.Redis.Affinity, labels),
					Tolerations:     rf.Spec.Redis.Tolerations,
					NodeSelector:    rf.Spec.Redis.NodeSelector,
					SecurityContext: getSecurityContext(rf.Spec.Redis.SecurityContext),
					ImagePullSecrets: rf.Spec.Redis.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "redis",
							Image:           rf.Spec.Redis.Image,
							ImagePullPolicy: pullPolicy(rf.Spec.Redis.ImagePullPolicy),
							Ports: []corev1.ContainerPort{
								{
									Name:          "redis",
									ContainerPort: 6379,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: volumeMounts,
							Command:      redisCommand,
							ReadinessProbe: &corev1.Probe{
								InitialDelaySeconds: graceTime,
								TimeoutSeconds:      5,
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh",
											"-c",
											"redis-cli -h $(hostname) ping",
										},
									},
								},
							},
							LivenessProbe: &corev1.Probe{
								InitialDelaySeconds: graceTime,
								TimeoutSeconds:      5,
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh",
											"-c",
											"redis-cli -h $(hostname) ping",
										},
									},
								},
							},
							Resources: rf.Spec.Redis.Resources,
							Lifecycle: &corev1.Lifecycle{
								PreStop: &corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{"/bin/sh", "/redis-shutdown/shutdown.sh"},
									},
								},
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	if rf.Spec.Redis.Storage.PersistentVolumeClaim != nil {
		if !rf.Spec.Redis.Storage.KeepAfterDeletion {
			// Set an owner reference so the persistent volumes are deleted when the RF is
			rf.Spec.Redis.Storage.PersistentVolumeClaim.OwnerReferences = ownerRefs
		}
		ss.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
			*rf.Spec.Redis.Storage.PersistentVolumeClaim,
		}
	}

	if rf.Spec.Redis.Exporter.Enabled {
		exporter := createRedisExporterContainer(rf)
		ss.Spec.Template.Spec.Containers = append(ss.Spec.Template.Spec.Containers, exporter)
	}

	return ss
}

func generateSentinelDeployment(rf *redisfailoverv1.RedisFailover, labels map[string]string, ownerRefs []metav1.OwnerReference) *appsv1.Deployment {
	name := GetSentinelName(rf)
	configMapName := GetSentinelName(rf)
	namespace := rf.Namespace

	sentinelCommand := getSentinelCommand(rf)
	selectorLabels := generateSelectorLabels(sentinelRoleName, rf.Name)
	labels = util.MergeLabels(labels, selectorLabels)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &rf.Spec.Sentinel.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: rf.Spec.Sentinel.PodAnnotations,
				},
				Spec: corev1.PodSpec{
					Affinity:        getAffinity(rf.Spec.Sentinel.Affinity, labels),
					Tolerations:     rf.Spec.Sentinel.Tolerations,
					NodeSelector:    rf.Spec.Sentinel.NodeSelector,
					SecurityContext: getSecurityContext(rf.Spec.Sentinel.SecurityContext),
					ImagePullSecrets: rf.Spec.Sentinel.ImagePullSecrets,
					InitContainers: []corev1.Container{
						{
							Name:            "sentinel-config-copy",
							Image:           rf.Spec.Sentinel.Image,
							ImagePullPolicy: pullPolicy(rf.Spec.Sentinel.ImagePullPolicy),
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "sentinel-config",
									MountPath: "/redis",
								},
								{
									Name:      "sentinel-config-writable",
									MountPath: "/redis-writable",
								},
							},
							Command: []string{
								"cp",
								fmt.Sprintf("/redis/%s", sentinelConfigFileName),
								fmt.Sprintf("/redis-writable/%s", sentinelConfigFileName),
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "sentinel",
							Image:           rf.Spec.Sentinel.Image,
							ImagePullPolicy: pullPolicy(rf.Spec.Sentinel.ImagePullPolicy),
							Ports: []corev1.ContainerPort{
								{
									Name:          "sentinel",
									ContainerPort: 26379,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "sentinel-config-writable",
									MountPath: "/redis",
								},
							},
							Command: sentinelCommand,
							ReadinessProbe: &corev1.Probe{
								InitialDelaySeconds: graceTime,
								TimeoutSeconds:      5,
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh",
											"-c",
											"redis-cli -h $(hostname) -p 26379 ping",
										},
									},
								},
							},
							LivenessProbe: &corev1.Probe{
								InitialDelaySeconds: graceTime,
								TimeoutSeconds:      5,
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh",
											"-c",
											"redis-cli -h $(hostname) -p 26379 ping",
										},
									},
								},
							},
							Resources: rf.Spec.Sentinel.Resources,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "sentinel-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
						},
						{
							Name: "sentinel-config-writable",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
}

func generatePodDisruptionBudget(name string, namespace string, labels map[string]string, ownerRefs []metav1.OwnerReference, minAvailable intstr.IntOrString) *policyv1beta1.PodDisruptionBudget {
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: policyv1beta1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	}
}

func generateResourceList(cpu string, memory string) corev1.ResourceList {
	resources := corev1.ResourceList{}
	if cpu != "" {
		resources[corev1.ResourceCPU], _ = resource.ParseQuantity(cpu)
	}
	if memory != "" {
		resources[corev1.ResourceMemory], _ = resource.ParseQuantity(memory)
	}
	return resources
}

func createRedisExporterContainer(rf *redisfailoverv1.RedisFailover) corev1.Container {
	container := corev1.Container{
		Name:            exporterContainerName,
		Image:           rf.Spec.Redis.Exporter.Image,
		ImagePullPolicy: pullPolicy(rf.Spec.Redis.Exporter.ImagePullPolicy),
		Env: []corev1.EnvVar{
			{
				Name: "REDIS_ALIAS",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "metrics",
				ContainerPort: exporterPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(exporterDefaultLimitCPU),
				corev1.ResourceMemory: resource.MustParse(exporterDefaultLimitMemory),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(exporterDefaultRequestCPU),
				corev1.ResourceMemory: resource.MustParse(exporterDefaultRequestMemory),
			},
		},
	}

	if rf.Spec.Auth.SecretPath != "" {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: "REDIS_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: rf.Spec.Auth.SecretPath,
					},
					Key: "password",
				},
			},
		})

	}

	return container
}

func getAffinity(affinity *corev1.Affinity, labels map[string]string) *corev1.Affinity {
	if affinity != nil {
		return affinity
	}

	// Return a SOFT anti-affinity
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						TopologyKey: hostnameTopologyKey,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labels,
						},
					},
				},
			},
		},
	}
}

func getSecurityContext(secctx *corev1.PodSecurityContext) *corev1.PodSecurityContext {
	if secctx != nil {
		return secctx
	}

	defaultUserAndGroup := int64(1000)
	runAsNonRoot := true

	return &corev1.PodSecurityContext{
		RunAsUser:    &defaultUserAndGroup,
		RunAsGroup:   &defaultUserAndGroup,
		RunAsNonRoot: &runAsNonRoot,
	}
}

func getQuorum(rf *redisfailoverv1.RedisFailover) int32 {
	return rf.Spec.Sentinel.Replicas/2 + 1
}

func getRedisVolumeMounts(rf *redisfailoverv1.RedisFailover) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      redisConfigurationVolumeName,
			MountPath: "/redis",
		},
		{
			Name:      redisShutdownConfigurationVolumeName,
			MountPath: "/redis-shutdown",
		},
		{
			Name:      getRedisDataVolumeName(rf),
			MountPath: "/data",
		},
	}

	return volumeMounts
}

func getRedisVolumes(rf *redisfailoverv1.RedisFailover) []corev1.Volume {
	configMapName := GetRedisName(rf)
	shutdownConfigMapName := GetRedisShutdownConfigMapName(rf)

	executeMode := int32(0744)
	volumes := []corev1.Volume{
		{
			Name: redisConfigurationVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		},
		{
			Name: redisShutdownConfigurationVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: shutdownConfigMapName,
					},
					DefaultMode: &executeMode,
				},
			},
		},
	}

	dataVolume := getRedisDataVolume(rf)
	if dataVolume != nil {
		volumes = append(volumes, *dataVolume)
	}

	return volumes
}

func getRedisDataVolume(rf *redisfailoverv1.RedisFailover) *corev1.Volume {
	// This will find the volumed desired by the user. If no volume defined
	// an EmptyDir will be used by default
	switch {
	case rf.Spec.Redis.Storage.PersistentVolumeClaim != nil:
		return nil
	case rf.Spec.Redis.Storage.EmptyDir != nil:
		return &corev1.Volume{
			Name: redisStorageVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: rf.Spec.Redis.Storage.EmptyDir,
			},
		}
	default:
		return &corev1.Volume{
			Name: redisStorageVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
	}
}

func getRedisDataVolumeName(rf *redisfailoverv1.RedisFailover) string {
	switch {
	case rf.Spec.Redis.Storage.PersistentVolumeClaim != nil:
		return rf.Spec.Redis.Storage.PersistentVolumeClaim.Name
	case rf.Spec.Redis.Storage.EmptyDir != nil:
		return redisStorageVolumeName
	default:
		return redisStorageVolumeName
	}
}

func getRedisCommand(rf *redisfailoverv1.RedisFailover) []string {
	if len(rf.Spec.Redis.Command) > 0 {
		return rf.Spec.Redis.Command
	}
	return []string{
		"redis-server",
		fmt.Sprintf("/redis/%s", redisConfigFileName),
	}
}

func getSentinelCommand(rf *redisfailoverv1.RedisFailover) []string {
	if len(rf.Spec.Sentinel.Command) > 0 {
		return rf.Spec.Sentinel.Command
	}
	return []string{
		"redis-server",
		fmt.Sprintf("/redis/%s", sentinelConfigFileName),
		"--sentinel",
	}
}

func pullPolicy(specPolicy corev1.PullPolicy) corev1.PullPolicy {
	if specPolicy == "" {
		return corev1.PullAlways
	}
	return specPolicy
}
