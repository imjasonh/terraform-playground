import os

import uvicorn
from fastapi import FastAPI


def create_app() -> FastAPI:
    app = FastAPI(title="pymage example")

    @app.get("/")
    def root() -> dict[str, str]:
        return {"status": "ok", "service": "pymage-example"}

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "healthy"}

    return app


def main() -> None:
    host = os.getenv("HOST", "0.0.0.0")
    port = int(os.getenv("PORT", "8080"))
    uvicorn.run(create_app(), host=host, port=port)
