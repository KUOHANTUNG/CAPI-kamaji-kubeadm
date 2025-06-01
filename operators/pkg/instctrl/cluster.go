package instctrl

import (
	"context"
	"fmt"

	"github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
	clv1alpha2 "github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
	clctx "github.com/netgroup-polito/CrownLabs/operators/pkg/context"
	"github.com/netgroup-polito/CrownLabs/operators/pkg/forge"
	"github.com/netgroup-polito/CrownLabs/operators/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	infrav1 "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InstanceReconciler enforces the Cluster API environment for a CrownLabs instance
func (r *InstanceReconciler) EnforceClusterEnvironment(ctx context.Context) error {
	// log := ctrl.LoggerFrom(ctx)
	// environment := clctx.EnvironmentFrom(ctx)
	// if environment.Mode == clv1alpha2.ModeStandard {
	// 	if err := r.EnforceCloudInitSecret(ctx); err != nil {
	// 		log.Error(err, "failed to enforce the cloud-init secret existence")
	// 		return err
	// 	}
	// }
	r.enforceCluster(ctx)
	r.enforceInfra(ctx)
	r.enforceControlPlane(ctx)
	r.enforceMachineDeployment(ctx)
	r.enforceKubevirtMachine(ctx)
	r.enforceBootstrap(ctx)
	return nil
}

// enforceCluster creates or updates the Cluster resource and sets its OwnerRef
func (r *InstanceReconciler) enforceCluster(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cl := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-cluster", cluster.Name),
			Namespace: instance.Namespace,
		},
	}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, cl, func() error {
		// ensure refs exist
		if cl.Spec.InfrastructureRef == nil {
			cl.Spec.InfrastructureRef = &corev1.ObjectReference{}
		}
		if cl.Spec.ControlPlaneRef == nil {
			cl.Spec.ControlPlaneRef = &corev1.ObjectReference{}
		}
		if cl.CreationTimestamp.IsZero() {
			// align infrastructure and controlPlane refs
			cl.Spec.InfrastructureRef.APIVersion = infrav1.GroupVersion.String()
			cl.Spec.InfrastructureRef.Kind = "KubevirtCluster"
			cl.Spec.InfrastructureRef.Name = fmt.Sprintf("%s-infra", cluster.Name)

			cl.Spec.ControlPlaneRef.APIVersion = controlplanev1.GroupVersion.String()
			if controlplane.Provider == v1alpha2.ProviderKubeadm {
				cl.Spec.ControlPlaneRef.Kind = "KubeadmControlPlane"
			} else {
				cl.Spec.ControlPlaneRef.Kind = "KamajiControlPlane"
			}
			cl.Spec.ControlPlaneRef.Name = fmt.Sprintf("%s-control-plane", cluster.Name)

			// align network and endpoint
			cl.Spec.ClusterNetwork = &capiv1.ClusterNetwork{
				Pods:     &capiv1.NetworkRanges{CIDRBlocks: clusternet.Pods.CIDRBlocks},
				Services: &capiv1.NetworkRanges{CIDRBlocks: clusternet.Services.CIDRBlocks},
			}
		}
		cl.SetLabels(forge.InstanceObjectLabels(cl.GetLabels(), instance))
		return ctrl.SetControllerReference(instance, cl, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to enforce cluster", "cluster", klog.KObj(cl))
		return err
	}
	log.V(utils.FromResult(res)).Info("Cluster enforced", "Cluster", klog.KObj(cl), "result", res)
	return nil
}

// enforceInfra creates or updates the KubevirtCluster resource and labels it for CAPI
func (r *InstanceReconciler) enforceInfra(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	infra := &infrav1.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-infra", cluster.Name),
			Namespace: instance.Namespace,
		},
	}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, infra, func() error {
		if infra.CreationTimestamp.IsZero() {
			infra.Spec.ControlPlaneServiceTemplate.Spec.Type = corev1.ServiceType(cluster.ServiceType)
		}
		if infra.Labels == nil {
			infra.Labels = map[string]string{}
		}
		infra.SetLabels(forge.InstanceObjectLabels(infra.GetLabels(), instance))
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce infrastructure", "infra", klog.KObj(infra))
		return err
	}
	log.V(utils.FromResult(res)).Info("Infrastructure enforced", "infra", klog.KObj(infra), "result", res)
	return nil
}

// enforceControlPlane creates or updates the KubeadmControlPlane resource and labels it
func (r *InstanceReconciler) enforceControlPlane(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	clusternet := cluster.ClusterNet
	controlplane := cluster.ControlPlane
	cp := &controlplanev1.KubeadmControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-control-plane", cluster.Name),
			Namespace: instance.Namespace,
		},
	}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, cp, func() error {

		if cp.CreationTimestamp.IsZero() {
			cp.Spec.Version = cluster.Version
			cp.Spec.Replicas = pt(int32(controlplane.Replicas))
			// align kubeadm specs
			cp.Spec.KubeadmConfigSpec.ClusterConfiguration = &bootstrapv1.ClusterConfiguration{
				Networking: bootstrapv1.Networking{
					DNSDomain:     fmt.Sprintf("%s.%s.local", instance.Name, instance.Namespace),
					PodSubnet:     clusternet.Pods.CIDRBlocks[0],
					ServiceSubnet: clusternet.Services.CIDRBlocks[0],
				},
			}
			cp.Spec.KubeadmConfigSpec.InitConfiguration = &bootstrapv1.InitConfiguration{
				NodeRegistration: bootstrapv1.NodeRegistrationOptions{CRISocket: "unix:///var/run/containerd/containerd.sock"},
			}
			cp.Spec.KubeadmConfigSpec.ClusterConfiguration.APIServer.CertSANs = controlplane.CertSANs
			cp.Spec.KubeadmConfigSpec.JoinConfiguration = &bootstrapv1.JoinConfiguration{NodeRegistration: bootstrapv1.NodeRegistrationOptions{CRISocket: "unix:///var/run/containerd/containerd.sock"}}
			// infra ref
			cp.Spec.MachineTemplate.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name)}
		}
		if cp.Labels == nil {
			cp.Labels = map[string]string{}
		}
		cp.SetLabels(forge.InstanceObjectLabels(cp.GetLabels(), instance))
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce controlplane", "cp", klog.KObj(cp))
		return err
	}
	log.V(utils.FromResult(res)).Info("ControlPlane enforced", "cp", klog.KObj(cp), "result", res)
	return nil
}

// enforceMachineDeployment creates or updates the MachineDeployment and labels it
func (r *InstanceReconciler) enforceMachineDeployment(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	machinedeployment := cluster.MachineDeploy
	md := &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md", cluster.Name), Namespace: instance.Namespace},
	}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, md, func() error {
		md.Spec.ClusterName = fmt.Sprintf("%s-cluster", cluster.Name)
		md.Spec.Replicas = pt(int32(machinedeployment.Replicas))
		md.Spec.Template.Spec.ClusterName = fmt.Sprintf("%s-cluster", cluster.Name)
		md.Spec.Template.Spec.Bootstrap.ConfigRef = &corev1.ObjectReference{APIVersion: bootstrapv1.GroupVersion.String(), Kind: "KubeadmConfigTemplate", Name: fmt.Sprintf("%s-md-bootstrap", cluster.Name)}
		md.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{APIVersion: infrav1.GroupVersion.String(), Kind: "KubevirtMachineTemplate", Name: fmt.Sprintf("%s-md-worker", cluster.Name)}
		md.Spec.Template.Spec.Version = &cluster.Version
		if md.Labels == nil {
			md.Labels = map[string]string{}
		}
		md.SetLabels(forge.InstanceObjectLabels(md.GetLabels(), instance))
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce machinedeployment")
		return err
	}
	return nil
}

// enforceKubevirtMachine creates or updates KubevirtMachineTemplates with RunStrategy and DV mapping
func (r *InstanceReconciler) enforceKubevirtMachine(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	controlplane := cluster.ControlPlane

	// worker template
	wmworker := infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-worker", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, &wmworker, func() error {
		if wmworker.CreationTimestamp.IsZero() {
			wmworker.Spec.Template.Spec.BootstrapCheckSpec.CheckStrategy = "ssh"

			vmSpec := ClusterVMSpec(environment)
			wmworker.Spec.Template.Spec.VirtualMachineTemplate.Spec = vmSpec
		}
		// label
		wmworker.Spec.Template.Spec.VirtualMachineTemplate.Spec.Running = ptr.To(instance.Spec.Running)
		if wmworker.Labels == nil {
			wmworker.Labels = map[string]string{}
		}
		wmworker.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce virtualmachine-worker")
		return err
	}
	log.V(utils.FromResult(res)).Info("virtualmachine-worker enforced")

	// control-plane template
	if controlplane.Provider == v1alpha2.ProviderKubeadm {
		wmcp := infrav1.KubevirtMachineTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name), Namespace: instance.Namespace}}
		res, err := ctrl.CreateOrUpdate(ctx, r.Client, &wmcp, func() error {

			if wmcp.CreationTimestamp.IsZero() {
				wmcp.Spec.Template.Spec.BootstrapCheckSpec.CheckStrategy = "ssh"

				vmSpec := ClusterVMSpec(environment)
				wmcp.Spec.Template.Spec.VirtualMachineTemplate.Spec = vmSpec
			}

			if wmcp.Labels == nil {
				wmcp.Labels = map[string]string{}
			}
			wmcp.Labels[capiv1.ClusterNameLabel] = fmt.Sprintf("%s-cluster", cluster.Name)
			return nil
		})
		if err != nil {
			log.Error(err, "failed to enforce virtualmachine-control-plane")
			return err
		}
		log.V(utils.FromResult(res)).Info("virtualmachine-control-plane enforced")
		vm := virtv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name), Namespace: instance.Namespace}}
		vmi := virtv1.VirtualMachineInstance{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-control-plane-machine", cluster.Name), Namespace: instance.Namespace}}
		if err = r.Get(ctx, client.ObjectKeyFromObject(&vmi), &vmi); client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to retrieve virtualmachineinstance", "virtualmachineinstance", klog.KObj(&vm))
			return err
		} else if err != nil {
			klog.Infof("VMI %s doesn't exist", instance.Name)
		}
		phase := r.RetrievePhaseFromVM(&vm, &vmi)

		if phase != instance.Status.Phase {
			log.Info("phase changed", "virtualmachine", klog.KObj(&vm),
				"previous", string(instance.Status.Phase), "current", string(phase))
			instance.Status.Phase = phase
		}
	}
	return nil
}

// enforceBootstrap creates or updates the KubeadmConfigTemplate and labels it
func (r *InstanceReconciler) enforceBootstrap(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	instance := clctx.InstanceFrom(ctx)
	environment := clctx.EnvironmentFrom(ctx)
	cluster := environment.Cluster
	bt := bootstrapv1.KubeadmConfigTemplate{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-md-bootstrap", cluster.Name), Namespace: instance.Namespace}}
	res, err := ctrl.CreateOrUpdate(ctx, r.Client, &bt, func() error {
		if bt.CreationTimestamp.IsZero() {
			bt.Spec.Template.Spec.JoinConfiguration = &bootstrapv1.JoinConfiguration{NodeRegistration: bootstrapv1.NodeRegistrationOptions{KubeletExtraArgs: map[string]string{}}}
		}
		if bt.Labels == nil {
			bt.Labels = map[string]string{}
		}
		bt.SetLabels(forge.InstanceObjectLabels(bt.GetLabels(), instance))
		return nil
	})
	if err != nil {
		log.Error(err, "failed to enforce bootstrap")
		return err
	}
	log.V(utils.FromResult(res)).Info("bootstrap enforced")
	return nil
}

// pt is a generic pointer helper
func pt[T any](v T) *T { return &v }
func ClusterVMSpec(environment *clv1alpha2.Environment) virtv1.VirtualMachineSpec {

	return virtv1.VirtualMachineSpec{
		RunStrategy: ptr.To(virtv1.RunStrategyAlways),
		Template: &virtv1.VirtualMachineInstanceTemplateSpec{
			Spec: ClusterVMISpec(environment),
		},
	}
}
func ClusterVMISpec(environment *clv1alpha2.Environment) virtv1.VirtualMachineInstanceSpec {
	return virtv1.VirtualMachineInstanceSpec{
		Domain: ClusterVMDomain(environment),
		Volumes: []virtv1.Volume{
			{
				Name: "containervolume",
				VolumeSource: virtv1.VolumeSource{
					ContainerDisk: &virtv1.ContainerDiskSource{
						Image: environment.Image,
					},
				},
			},
		},
		EvictionStrategy: ptr.To(virtv1.EvictionStrategyExternal),
	}
}

func ClusterVMDomain(environment *clv1alpha2.Environment) virtv1.DomainSpec {
	return virtv1.DomainSpec{
		CPU:       &virtv1.CPU{Cores: environment.Resources.CPU},
		Memory:    &virtv1.Memory{Guest: &environment.Resources.Memory},
		Resources: forge.VirtualMachineResources(environment),
		Devices: virtv1.Devices{
			NetworkInterfaceMultiQueue: ptr.To(true),
			Disks:                      []virtv1.Disk{forge.VolumeDiskTarget("containervolume")},
		},
	}
}
