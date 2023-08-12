addEventListener("fetch", (event) => {
    console.log("Hello from the service worker!");
    let value = KV.get("foo");
    event.respondWith(new Response(value,
        { headers: { "content-type": "text/plain" } }));
});
