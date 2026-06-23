use std::path::Path;
use std::sync::Arc;

use oci_distribution::Reference;

use crate::error::Result;
use crate::gz::{IndexedLayer, LayerSource};
use crate::metrics;
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
        metrics::mark_startup_begin();
        let (reference, layer_digests) = registry.resolve_layers(image).await?;
        let mut total_layer_bytes = 0u64;
        for digest in &layer_digests {
            total_layer_bytes = total_layer_bytes.saturating_add(
                registry
                    .layer_compressed_size(&reference, digest)
                    .await?,
            );
        }
        metrics::record_layer_sizes(total_layer_bytes);

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
