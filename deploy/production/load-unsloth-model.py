#!/usr/bin/env python3
"""Restore the 386GPT model after each Unsloth start; never log credentials."""

import json
import os
from pathlib import Path
import time
import urllib.error
import urllib.request


BASE_URL = "http://127.0.0.1:8888"
MODEL = "HauhauCS/Gemma-4-E2B-Uncensored-HauhauCS-Aggressive"
QUANT = "Q4_K_P"
CONTEXT = 66304


def main():
    key = (Path(os.environ["CREDENTIALS_DIRECTORY"]) / "unsloth-api-key").read_text().strip()
    if not key:
        raise RuntimeError("Unsloth API credential is empty")

    def request(path, body=None, timeout=10):
        req = urllib.request.Request(
            BASE_URL + path,
            data=json.dumps(body).encode() if body is not None else None,
            headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=timeout) as response:
            return json.load(response)

    def ready(status):
        # /v1/models can omit `quant` immediately after an API-initiated load;
        # inference/status reports the actual resident backend's quantization.
        return (
            status.get("active_model") == MODEL
            and MODEL in status.get("loaded", [])
            and status.get("gguf_variant") == QUANT
            and (status.get("context_length") or 0) >= 65536
        )

    print("Waiting for Unsloth's authenticated API...", flush=True)
    deadline = time.monotonic() + 120
    while True:
        try:
            status = request("/api/inference/status")
            break
        except urllib.error.HTTPError as error:
            if error.code < 500:
                raise RuntimeError(f"Unsloth authentication/API check failed: HTTP {error.code}") from None
        except (urllib.error.URLError, TimeoutError):
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError("Unsloth API did not become ready within 120 seconds")
        time.sleep(2)

    if not ready(status):
        print(f"Loading {MODEL} ({QUANT})...", flush=True)
        request("/api/inference/load", {
            "model_path": MODEL,
            "gguf_variant": QUANT,
            "max_seq_length": CONTEXT,
            "n_parallel": 4,
            "gpu_memory_mode": "auto",
        }, timeout=420)

    status = request("/api/inference/status")
    if not ready(status):
        details = {field: status.get(field) for field in
                   ("active_model", "loaded", "gguf_variant", "context_length")}
        raise RuntimeError("Gemma Q4_K_P did not load with at least 65,536 context tokens: "
                           + json.dumps(details))
    print(f"Unsloth ready: {MODEL} ({QUANT}), context >= 65536", flush=True)


if __name__ == "__main__":
    main()
