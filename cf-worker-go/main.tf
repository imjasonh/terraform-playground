terraform {
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare" }
  }
}

variable "name" { type = string }
variable "cloudflare_zone" { type = string }
variable "cloudflare_api_token" { type = string }
variable "cloudflare_account_id" { type = string }

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

// TODO: need to manually enable the workers.dev route in the dashboard
// use cloudflare_worker_route instead to set up routing
resource "cloudflare_worker_script" "script" {
  depends_on = [terraform_data.build]
  account_id = var.cloudflare_account_id
  name       = var.name
  content    = file("${path.module}/build/worker.mjs")

  // │ Error: error creating worker script: Wasm and blob bindings are not supported with modules; use imports instead (10021)
  module = false

  webassembly_binding {
    name   = "WASM"
    module = filebase64("./build/app.wasm")
  }
}

/*
resource "cloudflare_worker_route" "route" {
  zone_id     = data.cloudflare_zone.zone.id
  pattern     = "${var.name}.${var.cloudflare_zone}/*"
  script_name = cloudflare_worker_script.script.name
}

data "cloudflare_zone" "zone" {
  name = var.cloudflare_zone
}
*/

resource "terraform_data" "build" {
  # Changes to *.go files requires re-building it.
  input = {
    # https://stackoverflow.com/questions/51138667/can-terraform-watch-a-directory-for-changes
    dir_sha1 = sha1(join("", [for f in fileset("${path.module}", "*.go") : filesha1("${path.module}/${f}")]))
  }

  provisioner "local-exec" {
    command = <<EOC
go run github.com/syumai/workers/cmd/workers-assets-gen@v0.18.0 -mode=go
GOOS=js GOARCH=wasm go build -o ./build/app.wasm .
EOC
  }
}
