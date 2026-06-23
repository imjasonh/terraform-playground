use std::path::Path;
use std::sync::Arc;

use oci_distribution::Reference;

use crate::error::Result;
use crate::gz::{IndexedLayer, LayerSource};
use crate::overlay::OverlayFs;
use crate::registry::{LocalBlob, RegistryClient};

/// Parsed container image reference.
#[derive(Debug, Clone)]
pub struct ImageRef {
    pub reference: Reference,
    pub layer_digests: Vec<String>,
}

/// High-level API: resolve an OCI image and build a lazy overlay filesystem.
pub struct ImageFs {
    pub image: ImageRef,
    pub overlay: Arc<OverlayFs>,
}

impl ImageFs {
    pub async fn open(
        image: &str,
        cache_dir: impl AsRef<Path>,
        registry: &mut RegistryClient,
    ) -> Result<Self> {
        let (reference, layer_digests) = registry.resolve_layers(image).await?;
        let mut layers = Vec::new();
        for digest in &layer_digests {
            let blob = registry
                .open_blob(&reference, digest)
                .await?;
            let layer = IndexedLayer::open(
                digest,
                LayerSource::Remote(blob),
                cache_dir.as_ref(),
            )?;
            layers.push(Arc::new(layer));
        }
        let overlay = OverlayFs::from_layers(layers)?;
        Ok(Self {
            image: ImageRef {
                reference,
                layer_digests,
            },
            overlay,
        })
    }

    pub fn open_local_layer(
        digest: &str,
        blob_path: impl AsRef<Path>,
        cache_dir: impl AsRef<Path>,
    ) -> Result<Self> {
        let layer = Arc::new(IndexedLayer::open(
            digest,
            LayerSource::Local(LocalBlob::open(blob_path)?),
            cache_dir.as_ref(),
        )?);
        let overlay = OverlayFs::from_layers(vec![layer])?;
        Ok(Self {
            image: ImageRef {
                reference: "local/fixture:latest".parse().unwrap(),
                layer_digests: vec![digest.to_string()],
            },
            overlay,
        })
    }
}
