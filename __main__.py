"""
Pulumi AWS EKS Infrastructure for eBPF-based Observability (Pixie)
"""

import json
import os
import pulumi
import pulumi_aws as aws
import pulumi_awsx as awsx
import pulumi_eks as eks

# ------------------------------------------------------------------------------
# 1. Cost-Optimized & Public-Facing Networking (VPC)
# ------------------------------------------------------------------------------
# We configure the VPC to contain PUBLIC SUBNETS ONLY across 2 Availability Zones (AZs)
# to completely avoid the overhead and costs of NAT Gateways.
# An Internet Gateway (IGW) is natively attached and public route tables
# route all outbound traffic (0.0.0.0/0) directly to the IGW.
# DNS hostnames and resolution are enabled on the VPC to ensure node communication.

vpc = awsx.ec2.Vpc("ebpf-vpc",
    cidr_block="10.0.0.0/16",
    enable_dns_hostnames=True,
    enable_dns_support=True,
    subnet_specs=[
        awsx.ec2.SubnetSpecArgs(
            type=awsx.ec2.SubnetType.PUBLIC,
            name="public",
            cidr_mask=24,
        )
    ],
    number_of_availability_zones=2,
    nat_gateways=awsx.ec2.NatGatewayConfigurationArgs(
        strategy=awsx.ec2.NatGatewayStrategy.NONE,
    )
)

# ------------------------------------------------------------------------------
# 2. IAM Role for EKS Managed Node Group
# ------------------------------------------------------------------------------
# Explicitly define the IAM role for the Managed Node Group to attach necessary
# policies for EKS worker nodes, CNI plugin, and ECR access.

node_role = aws.iam.Role("ebpf-node-role",
    assume_role_policy=json.dumps({
        "Version": "2012-10-17",
        "Statement": [{
            "Action": "sts:AssumeRole",
            "Effect": "Allow",
            "Principal": {
                "Service": "ec2.amazonaws.com"
            }
        }]
    })
)

policies = [
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
]

for i, policy in enumerate(policies):
    aws.iam.RolePolicyAttachment(f"ebpf-node-role-policy-{i}",
        role=node_role.name,
        policy_arn=policy
    )

# ------------------------------------------------------------------------------
# 3. EKS Control Plane & IAM Configuration
# ------------------------------------------------------------------------------
# Provision an EKS cluster with both public and private endpoint access.
# Private access ensures fast, secure node-to-control-plane communication within the VPC.
# Public access allows local kubectl administrative operations.
# OIDC provider is configured to support service-account-level IAM roles (IRSA) for Pixie.
#
# PINNED KUBERNETES VERSION — this is the actual fix for the recurring
# "Table 'http_events' not found" problem:
#
# AWS ties the kernel that ships in the AL2023 EKS-optimized AMI to the
# cluster's Kubernetes MINOR version, not to AL2023 as a whole:
#   - EKS 1.31 / 1.32  -> AL2023 AMI on Linux kernel 6.1
#   - EKS 1.33 / 1.34 / 1.35 -> AL2023 AMI on Linux kernel 6.12
#   - EKS 1.36         -> AL2023 AMI on Linux kernel 6.18
# (source: awslabs/amazon-eks-ami release changelogs)
#
# `pulumi_eks.Cluster` uses the newest available EKS version whenever
# `version` is left unset. That's why re-running `pulumi up` on a fresh
# stack can silently hand you a newer kernel each time, even with no
# code changes. Pixie's currently released (non pre-release) socket
# tracer relies on kernel headers bundled for the 6.1 line to compile
# its BPF program; on 6.12/6.18 that compile step can fail, so tables
# like http_events / conn_stats never get created — no eBPF token issue
# involved, it's a header-version mismatch.
#
# Pinning to 1.32 keeps you on kernel 6.1 deterministically, so the
# stable `px deploy` (no pre-release build needed) keeps working across
# rebuilds. Note: as of writing, 1.32 is in EKS *Extended* Support
# (small added per-cluster-hour cost). If you'd rather stay on Standard
# Support and accept the small residual risk of needing a newer Vizier
# pre-release on 6.12, use "1.33" instead.

EKS_VERSION = "1.32"

cluster = eks.Cluster("ebpf-eks-cluster",
    vpc_id=vpc.vpc_id,
    public_subnet_ids=vpc.public_subnet_ids,
    # Pin the control plane version so the node AMI/kernel doesn't drift
    # to a newer default on future `pulumi up` runs.
    version=EKS_VERSION,
    # Ensure fast, secure, intra-VPC node-to-control-plane communication
    endpoint_private_access=True,
    # Publicly accessible for local kubectl administration
    endpoint_public_access=True,
    # Automatically configure OIDC provider for IRSA support
    create_oidc_provider=True,
    # Ensure public IPs are auto-assigned so nodes can pull images and OS updates directly
    node_associate_public_ip_address=True,
    skip_default_node_group=True,
    instance_roles=[node_role]
)

# ------------------------------------------------------------------------------
# 4. eBPF-Ready Managed Node Group
# ------------------------------------------------------------------------------
# Provision instances with 't3.large' to meet memory-intensive demands of Pixie's PEM agents.
# Explicitly run on public subnets.
#
# AMI type is Amazon Linux 2023 standard (AL2023_x86_64_STANDARD). Combined
# with `version=EKS_VERSION` below, this locks the node group to the same
# kernel 6.1 AL2023 build as the control plane, instead of whatever AL2023
# build happens to be current for the default EKS version at deploy time.

managed_node_group = aws.eks.NodeGroup("ebpf-node-group",
    cluster_name=cluster.eks_cluster.name,
    node_group_name="ebpf-nodes",
    node_role_arn=node_role.arn,
    subnet_ids=vpc.public_subnet_ids,
    # Pin the node group to the same Kubernetes version as the control
    # plane, so it keeps resolving to the kernel-6.1 AL2023 AMI.
    version=EKS_VERSION,
    scaling_config=aws.eks.NodeGroupScalingConfigArgs(
        desired_size=2,
        min_size=2,
        max_size=3,
    ),
    instance_types=["t3.large"],
    ami_type="AL2023_x86_64_STANDARD",
)

# ------------------------------------------------------------------------------
# 5. Post-Provisioning Automation (Local Kubeconfig)
# ------------------------------------------------------------------------------
# Export the fully resolved kubeconfig as a Pulumi stack output.
pulumi.export("kubeconfig", cluster.kubeconfig)
pulumi.export("eksVersion", EKS_VERSION)

# Post-deployment Python file-system hook inside the program.
# Upon successful execution of Pulumi, this automatically writes the formatted
# kubeconfig payload to a local file named `kubeconfig.yaml` in the active workspace.
def write_kubeconfig(kc):
    if kc:
        file_path = os.path.join(os.getcwd(), "kubeconfig.yaml")
        try:
            with open(file_path, "w") as f:
                # EKS kubeconfig object provided by Pulumi is a dict
                # JSON representation is valid YAML and perfectly compatible with kubectl
                json.dump(kc, f, indent=4)
            print(f"✅ Successfully wrote kubeconfig to {file_path}")
        except Exception as e:
            print(f"⚠️ Failed to write kubeconfig.yaml: {e}")

# Apply the side effect once the kubeconfig output is fully resolved
cluster.kubeconfig.apply(write_kubeconfig)