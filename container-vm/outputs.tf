output "instance_template_self_link" {
  description = "The self-link of the created instance template. Use this to deploy VMs or MIGs."
  value       = google_compute_instance_template.container_vm_template.self_link
}

output "instance_template_name" {
  description = "The name of the created instance template."
  value       = google_compute_instance_template.container_vm_template.name
}

