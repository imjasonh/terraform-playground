use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::Path;
use std::sync::Arc;
use std::thread;

use fc_oci_fs::render_prometheus;
use log::{error, info};

pub fn spawn_metrics_server(addr: &str, cache_dir: Arc<Path>) -> thread::JoinHandle<()> {
    let addr = addr.to_string();
    thread::Builder::new()
        .name("fc-metrics".into())
        .spawn(move || {
            let listener = match TcpListener::bind(&addr) {
                Ok(l) => l,
                Err(e) => {
                    error!("metrics server failed to bind {addr}: {e}");
                    return;
                }
            };
            info!("prometheus metrics on http://{addr}/metrics");
            for stream in listener.incoming() {
                match stream {
                    Ok(mut stream) => {
                        if let Err(e) = handle_connection(&mut stream, &cache_dir) {
                            error!("metrics connection error: {e}");
                        }
                    }
                    Err(e) => error!("metrics accept error: {e}"),
                }
            }
        })
        .expect("spawn metrics thread")
}

fn handle_connection(stream: &mut TcpStream, cache_dir: &Path) -> std::io::Result<()> {
    let mut buf = [0u8; 1024];
    let n = stream.read(&mut buf)?;
    let req = String::from_utf8_lossy(&buf[..n]);
    let is_metrics = req.lines().next().is_some_and(|l| l.contains("GET /metrics"));
    if !is_metrics {
        let resp = "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n";
        stream.write_all(resp.as_bytes())?;
        return Ok(());
    }
    let body = render_prometheus(cache_dir);
    let resp = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: text/plain; version=0.0.4\r\nContent-Length: {}\r\n\r\n{}",
        body.len(),
        body
    );
    stream.write_all(resp.as_bytes())?;
    Ok(())
}
