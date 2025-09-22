terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}

variable "cluster_name" {
  type    = string
  default = "jason-eks-cluster2"
}

variable "k8s_version" {
  type    = string
  default = "1.33"
}

provider "aws" { region = "us-west-2" }

data "aws_ami" "chainguard" {
  executable_users = ["self"]
  most_recent      = true
  name_regex       = "chainguard-eks-${var.k8s_version}-dev-x86_64-.*"
  owners           = ["aws-marketplace"]
}

output "chainguard_ami_id" { value = data.aws_ami.chainguard.id }

# Get available AZs
data "aws_availability_zones" "available" { state = "available" }

# VPC
resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name        = "jason-eks-vpc"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Internet Gateway
resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name        = "jason-eks-igw"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Public subnets for EKS control plane
resource "aws_subnet" "public" {
  count = 3

  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.${count.index + 1}.0/24"
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name                     = "jason-eks-public-${count.index + 1}"
    Environment              = "dev"
    Terraform                = "true"
    "kubernetes.io/role/elb" = "1"
  }
}

# Private subnets for EKS worker nodes
resource "aws_subnet" "private" {
  count = 3

  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.0.${count.index + 10}.0/24"
  availability_zone = data.aws_availability_zones.available.names[count.index]

  tags = {
    Name                              = "jason-eks-private-${count.index + 1}"
    Environment                       = "dev"
    Terraform                         = "true"
    "kubernetes.io/role/internal-elb" = "1"
  }
}

# Elastic IPs for NAT gateways
resource "aws_eip" "nat" {
  count = 3

  domain     = "vpc"
  depends_on = [aws_internet_gateway.main]

  tags = {
    Name        = "jason-eks-nat-eip-${count.index + 1}"
    Environment = "dev"
    Terraform   = "true"
  }
}

# NAT gateways
resource "aws_nat_gateway" "main" {
  count = 3

  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = {
    Name        = "jason-eks-nat-${count.index + 1}"
    Environment = "dev"
    Terraform   = "true"
  }

  depends_on = [aws_internet_gateway.main]
}

# Route table for public subnets
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name        = "jason-eks-public-rt"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Route table associations for public subnets
resource "aws_route_table_association" "public" {
  count = 3

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Route tables for private subnets
resource "aws_route_table" "private" {
  count = 3

  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main[count.index].id
  }

  tags = {
    Name        = "jason-eks-private-rt-${count.index + 1}"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Route table associations for private subnets
resource "aws_route_table_association" "private" {
  count = 3

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# Data sources for EKS setup
data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}
data "aws_iam_session_context" "current" {
  arn = data.aws_caller_identity.current.arn
}

# OIDC thumbprint for GitHub OIDC (standard thumbprint)
data "tls_certificate" "cluster" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

# IAM role for EKS cluster
resource "aws_iam_role" "cluster" {
  name = "${var.cluster_name}-cluster-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "eks.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name        = "${var.cluster_name}-cluster-role"
    Environment = "dev"
    Terraform   = "true"
  }
}

resource "aws_iam_role_policy_attachment" "cluster_policy" {
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKSClusterPolicy"
  role       = aws_iam_role.cluster.name
}

# IAM role for EKS node group
resource "aws_iam_role" "node_group" {
  name = "${var.cluster_name}-node-group-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name        = "${var.cluster_name}-node-group-role"
    Environment = "dev"
    Terraform   = "true"
  }
}

resource "aws_iam_role_policy_attachment" "node_group_worker_policy" {
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKSWorkerNodePolicy"
  role       = aws_iam_role.node_group.name
}

resource "aws_iam_role_policy_attachment" "node_group_cni_policy" {
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKS_CNI_Policy"
  role       = aws_iam_role.node_group.name
}

resource "aws_iam_role_policy_attachment" "node_group_registry_policy" {
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
  role       = aws_iam_role.node_group.name
}

# OIDC provider for IRSA
resource "aws_iam_openid_connect_provider" "cluster" {
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.cluster.certificates[0].sha1_fingerprint]
  url             = aws_eks_cluster.this.identity[0].oidc[0].issuer

  tags = {
    Name        = "${var.cluster_name}-oidc-provider"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Security group for EKS cluster
resource "aws_security_group" "cluster" {
  name_prefix = "${var.cluster_name}-cluster-"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name        = "${var.cluster_name}-cluster-sg"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Security group for EKS node group
resource "aws_security_group" "node_group" {
  name_prefix = "${var.cluster_name}-node-group-"
  vpc_id      = aws_vpc.main.id

  tags = {
    Name        = "${var.cluster_name}-node-group-sg"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Security group rules for cluster
resource "aws_security_group_rule" "cluster_ingress_node_https" {
  description              = "Node groups to cluster API"
  from_port                = 443
  protocol                 = "tcp"
  security_group_id        = aws_security_group.cluster.id
  source_security_group_id = aws_security_group.node_group.id
  to_port                  = 443
  type                     = "ingress"
}

resource "aws_security_group_rule" "cluster_egress_node" {
  description              = "Cluster API to node groups"
  from_port                = 1025
  protocol                 = "tcp"
  security_group_id        = aws_security_group.cluster.id
  source_security_group_id = aws_security_group.node_group.id
  to_port                  = 65535
  type                     = "egress"
}

resource "aws_security_group_rule" "cluster_egress_node_https" {
  description              = "Cluster API to node groups"
  from_port                = 443
  protocol                 = "tcp"
  security_group_id        = aws_security_group.cluster.id
  source_security_group_id = aws_security_group.node_group.id
  to_port                  = 443
  type                     = "egress"
}

# Security group rules for node group
resource "aws_security_group_rule" "node_group_ingress_self" {
  description       = "Node to node ingress"
  from_port         = 0
  protocol          = "-1"
  security_group_id = aws_security_group.node_group.id
  self              = true
  to_port           = 65535
  type              = "ingress"
}


resource "aws_security_group_rule" "node_group_ingress_cluster_https" {
  description              = "Cluster API to node groups"
  from_port                = 443
  protocol                 = "tcp"
  security_group_id        = aws_security_group.node_group.id
  source_security_group_id = aws_security_group.cluster.id
  to_port                  = 443
  type                     = "ingress"
}

resource "aws_security_group_rule" "node_group_ingress_cluster" {
  description              = "Cluster API to node groups"
  from_port                = 1025
  protocol                 = "tcp"
  security_group_id        = aws_security_group.node_group.id
  source_security_group_id = aws_security_group.cluster.id
  to_port                  = 65535
  type                     = "ingress"
}

resource "aws_security_group_rule" "node_group_egress_all" {
  description       = "Node group egress"
  from_port         = 0
  protocol          = "-1"
  security_group_id = aws_security_group.node_group.id
  cidr_blocks       = ["0.0.0.0/0"]
  to_port           = 0
  type              = "egress"
}

# CloudWatch log group for EKS cluster
resource "aws_cloudwatch_log_group" "cluster" {
  name              = "/aws/eks/${var.cluster_name}/cluster"
  retention_in_days = 7

  tags = {
    Name        = "${var.cluster_name}-cluster-logs"
    Environment = "dev"
    Terraform   = "true"
  }
}

# EKS cluster
resource "aws_eks_cluster" "this" {
  name     = var.cluster_name
  role_arn = aws_iam_role.cluster.arn
  version  = var.k8s_version

  vpc_config {
    endpoint_private_access = false
    endpoint_public_access  = true
    public_access_cidrs     = ["0.0.0.0/0"]
    subnet_ids              = concat(aws_subnet.private[*].id, aws_subnet.public[*].id)
    security_group_ids      = [aws_security_group.cluster.id]
  }

  # Set authentication mode to use access entries API
  access_config {
    authentication_mode = "API_AND_CONFIG_MAP"
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator"]

  depends_on = [
    aws_iam_role_policy_attachment.cluster_policy,
    aws_cloudwatch_log_group.cluster,
  ]

  tags = {
    Name        = var.cluster_name
    Environment = "dev"
    Terraform   = "true"
  }
}

# EKS add-ons
resource "aws_eks_addon" "vpc_cni" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "vpc-cni"

  depends_on = [aws_eks_cluster.this]

  tags = {
    Environment = "dev"
    Terraform   = "true"
  }
}

resource "aws_eks_addon" "eks_pod_identity_agent" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "eks-pod-identity-agent"

  depends_on = [aws_eks_cluster.this, aws_eks_addon.vpc_cni]

  tags = {
    Environment = "dev"
    Terraform   = "true"
  }
}

resource "aws_eks_addon" "coredns" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "coredns"

  depends_on = [aws_eks_node_group.chainguard]

  tags = {
    Environment = "dev"
    Terraform   = "true"
  }
}

resource "aws_eks_addon" "kube_proxy" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "kube-proxy"

  depends_on = [aws_eks_node_group.chainguard]

  tags = {
    Environment = "dev"
    Terraform   = "true"
  }
}

# Launch template for custom AMI
resource "aws_launch_template" "chainguard" {
  name_prefix   = "${var.cluster_name}-chainguard-"
  image_id      = data.aws_ami.chainguard.id
  instance_type = "m7a.xlarge"

  vpc_security_group_ids = [aws_security_group.node_group.id]

  user_data = base64encode(yamlencode({
    apiVersion = "node.eks.aws/v1alpha1"
    kind       = "NodeConfig"
    spec = {
      cluster = {
        name                 = aws_eks_cluster.this.name
        apiServerEndpoint    = aws_eks_cluster.this.endpoint
        certificateAuthority = aws_eks_cluster.this.certificate_authority[0].data
        cidr                 = "10.100.0.0/16"
      }
      kubelet = {
        config = {
          clusterDNS = ["10.100.0.10"]
        }
      }
    }
  }))

  tag_specifications {
    resource_type = "instance"
    tags = {
      Name        = "${var.cluster_name}-chainguard-node"
      Environment = "dev"
      Terraform   = "true"
    }
  }

  tags = {
    Name        = "${var.cluster_name}-chainguard-lt"
    Environment = "dev"
    Terraform   = "true"
  }
}

# EKS managed node group
resource "aws_eks_node_group" "chainguard" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "chainguard"
  node_role_arn   = aws_iam_role.node_group.arn
  subnet_ids      = aws_subnet.private[*].id

  ami_type      = "CUSTOM"
  capacity_type = "ON_DEMAND"

  launch_template {
    id      = aws_launch_template.chainguard.id
    version = aws_launch_template.chainguard.latest_version
  }

  scaling_config {
    desired_size = 1
    max_size     = 2
    min_size     = 1
  }

  update_config {
    max_unavailable_percentage = 33
  }

  depends_on = [
    aws_iam_role_policy_attachment.node_group_worker_policy,
    aws_iam_role_policy_attachment.node_group_cni_policy,
    aws_iam_role_policy_attachment.node_group_registry_policy,
    aws_eks_addon.vpc_cni,
    aws_eks_addon.eks_pod_identity_agent,
  ]

  tags = {
    Name        = "${var.cluster_name}-chainguard"
    Environment = "dev"
    Terraform   = "true"
  }
}

# Cluster access entry for admin permissions
resource "aws_eks_access_entry" "cluster_creator" {
  cluster_name      = aws_eks_cluster.this.name
  principal_arn     = data.aws_iam_session_context.current.issuer_arn
  kubernetes_groups = []
  type              = "STANDARD"

  tags = {
    Environment = "dev"
    Terraform   = "true"
  }
}

resource "aws_eks_access_policy_association" "cluster_creator" {
  cluster_name  = aws_eks_cluster.this.name
  policy_arn    = "arn:${data.aws_partition.current.partition}:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
  principal_arn = data.aws_iam_session_context.current.issuer_arn

  access_scope {
    type = "cluster"
  }
}

# Outputs
output "cluster_endpoint" {
  description = "Endpoint for EKS control plane"
  value       = aws_eks_cluster.this.endpoint
}

output "cluster_security_group_id" {
  description = "Security group ID attached to the EKS cluster"
  value       = aws_eks_cluster.this.vpc_config[0].cluster_security_group_id
}

output "cluster_oidc_issuer_url" {
  description = "The URL on the EKS cluster for the OpenID Connect identity provider"
  value       = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

output "cluster_version" {
  description = "The Kubernetes version for the EKS cluster"
  value       = aws_eks_cluster.this.version
}

output "cluster_platform_version" {
  description = "Platform version for the EKS cluster"
  value       = aws_eks_cluster.this.platform_version
}

output "cluster_status" {
  description = "Status of the EKS cluster"
  value       = aws_eks_cluster.this.status
}
