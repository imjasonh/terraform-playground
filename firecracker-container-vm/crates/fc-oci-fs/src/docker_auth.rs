use docker_credential::DockerCredential;
use oci_distribution::secrets::RegistryAuth;
use oci_distribution::Reference;

use crate::error::{Error, Result};

/// Resolved registry credentials suitable for both `oci-distribution` and raw HTTP.
#[derive(Debug, Clone)]
pub enum ResolvedAuth {
    Anonymous,
    Basic { username: String, password: String },
    Bearer(String),
}

impl ResolvedAuth {
    pub fn for_reference(reference: &Reference) -> Result<Self> {
        let keys = registry_lookup_keys(reference);
        for key in keys {
            match docker_credential::get_credential(&key) {
                Ok(DockerCredential::UsernamePassword(username, password)) => {
                    return Ok(ResolvedAuth::Basic { username, password });
                }
                Ok(DockerCredential::IdentityToken(token)) => {
                    return Ok(ResolvedAuth::Bearer(token));
                }
                Err(docker_credential::CredentialRetrievalError::ConfigNotFound)
                | Err(docker_credential::CredentialRetrievalError::NoCredentialConfigured) => {
                    continue
                }
                Err(e) => return Err(Error::Registry(format!("docker credential {key}: {e}"))),
            }
        }
        Ok(ResolvedAuth::Anonymous)
    }

    pub fn to_oci_auth(&self) -> RegistryAuth {
        match self {
            ResolvedAuth::Anonymous => RegistryAuth::Anonymous,
            ResolvedAuth::Basic { username, password } => {
                RegistryAuth::Basic(username.clone(), password.clone())
            }
            // oci-distribution only supports Basic; identity tokens are applied on HTTP directly.
            ResolvedAuth::Bearer(token) => RegistryAuth::Basic(String::new(), token.clone()),
        }
    }

    pub fn authorization_header(&self) -> Option<String> {
        match self {
            ResolvedAuth::Anonymous => None,
            ResolvedAuth::Basic { username, password } => Some(basic_auth_header(username, password)),
            ResolvedAuth::Bearer(token) => Some(format!("Bearer {token}")),
        }
    }
}

fn basic_auth_header(username: &str, password: &str) -> String {
    use base64::Engine as _;
    let raw = format!("{username}:{password}");
    format!(
        "Basic {}",
        base64::engine::general_purpose::STANDARD.encode(raw.as_bytes())
    )
}

/// Keys tried when looking up `~/.docker/config.json` / cred helpers for a reference.
fn registry_lookup_keys(reference: &Reference) -> Vec<String> {
    let registry = reference.resolve_registry();
    let mut keys = Vec::new();
    if registry == "docker.io" || registry == "index.docker.io" {
        keys.push("https://index.docker.io/v1/".to_string());
    }
    keys.push(format!("https://{registry}"));
    keys.push(format!("https://{registry}/v1/"));
    keys.push(registry.to_string());
    keys
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn docker_hub_lookup_keys() {
        let reference: Reference = "docker.io/library/alpine:latest".parse().unwrap();
        let keys = registry_lookup_keys(&reference);
        assert!(keys.iter().any(|k| k.contains("index.docker.io")));
    }
}
