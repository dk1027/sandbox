# How to Run Qwen Locally on This MacBook Pro with MLX and OrbStack

Last checked: 2026-06-23.

This guide is tailored for this machine and runtime:

- MacBook Pro `Mac17,8`
- Apple M5 Pro, 18 CPU cores
- 64 GB unified memory
- macOS 26.5.1
- Docker context: `orbstack`
- Docker CLI: 29.4.0

The goal is to run a Qwen model as a host macOS process and let an agent running inside an OrbStack Docker container call it over an OpenAI-compatible local HTTP API.

## Recommendation

Use **MLX first** for local development on this Apple Silicon machine.

Recommended default model:

```text
mlx-community/Qwen3.6-27B-OptiQ-4bit
```

Why this one:

- It is an MLX-native, Apple Silicon-oriented quant.
- It is text-generation focused, which matches a coding/local agent better than a vision-first path.
- It is smaller on disk than the Qwen3.6-35B-A3B OptiQ build: about 20 GB versus about 24.7 GB.
- Qwen's own model card reports Qwen3.6-27B ahead of Qwen3.6-35B-A3B on several agent/coding benchmarks, including SWE-bench Verified, Terminal-Bench 2.0, SkillsBench, and QwenWebBench.

Use `mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit` instead if you specifically want the open-weight MoE Qwen3.6 model that was originally discussed. It is still a good choice, but for this machine and local coding-agent use, I would start with 27B OptiQ.

Keep `llama.cpp` as the fallback if the agent hits OpenAI API compatibility issues, strict JSON issues, or tool-call parsing problems.

## Qwen Model Options

### Best Default for Local Coding Agent

```text
mlx-community/Qwen3.6-27B-OptiQ-4bit
```

Use this first. It is a mixed-precision 4-bit MLX quant for Apple Silicon. The card reports a 20 GB MLX artifact and includes an MTP head usable through `mlx-optiq` for speculative decoding.

Tradeoff: dense 27B means more active compute per token than the MoE model, so benchmark it against 35B-A3B on your prompts if latency matters.

### Best MoE / Original Qwen3.6 Target

```text
mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit
```

Use this if you specifically want Qwen3.6-35B-A3B. It is 35B total parameters with about 3B active parameters and a mixed 4-bit/8-bit OptiQ quant. The card reports about 24.7 GB for the MLX artifact and an MTP head for `mlx-optiq`.

Tradeoff: it is larger in memory footprint than the 27B OptiQ build and Qwen's benchmark table puts 27B ahead on many coding-agent tasks.

### More Accurate but Heavier

```text
unsloth/Qwen3.6-35B-A3B-MLX-8bit
```

Use this only if you want an 8-bit MLX variant and can tolerate higher memory pressure. The card reports about 37.7 GB on disk. On a 64 GB machine, that leaves much less room for KV cache, browser, IDE, Docker containers, and other development tools.

### Vision / Image-Text Work

```text
mlx-community/Qwen3.6-27B-4bit
mlx-community/Qwen3.6-35B-A3B-4bit
```

Use these if you need image input. Their model cards show `mlx-vlm` usage. For a text/coding agent, prefer the OptiQ text-generation variants above.

### Older / Smaller Qwen Options

Qwen3 and Qwen2.5 models have many MLX conversions and are useful when you need speed over top quality:

- `Qwen3-14B` or `Qwen3-8B` MLX quants: faster and easier on memory.
- `Qwen2.5-Coder-32B` MLX/GGUF variants: still useful for code, but older than Qwen3.6.

Use these only if Qwen3.6 is too slow or too memory-hungry for your workflow.

## Install uv and Python 3.14

Install `uv` first:

```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

Then install or upgrade the uv-managed Python to 3.14:

```bash
uv python install 3.14
uv python pin 3.14
uv run python --version
```

The version check should report Python 3.14.x. On this machine, it reports Python 3.14.6.

## Install MLX LM

If `mlx_lm.server --help` prints a warning like this:

```text
NotOpenSSLWarning: urllib3 v2 only supports OpenSSL 1.1.1+, currently the 'ssl' module is compiled with 'LibreSSL 2.8.3'
```

then the `mlx-lm` tool was installed before uv was using the newer Python. On this machine, the old launcher pointed at a uv tool venv using Python 3.9.6 with LibreSSL 2.8.3.

After installing/pinning Python 3.14 with uv, reinstall the tool normally:

```bash
uv tool uninstall mlx-lm
uv tool install mlx-lm
```

Confirm the launcher and SSL backend:

```bash
head -n 1 "$(which mlx_lm.server)"
MLX_TOOL_PY="$(head -n 1 "$(which mlx_lm.server)" | sed 's/^#!//')"
"$MLX_TOOL_PY" --version
"$MLX_TOOL_PY" -c 'import ssl; print(ssl.OPENSSL_VERSION)'
mlx_lm.server --help
```

The launcher should no longer point at a Python 3.9 uv tool environment.

## Start the MLX Server

Start with the recommended 27B OptiQ model:

```bash
mlx_lm.server \
  --model mlx-community/Qwen3.6-27B-OptiQ-4bit \
  --host 127.0.0.1 \
  --port 8080
```

If your agent container cannot reach a loopback-bound server through OrbStack bridge networking, either bind to all interfaces:

```bash
mlx_lm.server \
  --model mlx-community/Qwen3.6-27B-OptiQ-4bit \
  --host 0.0.0.0 \
  --port 8080
```

or run the agent container with OrbStack host networking and keep the MLX server on `127.0.0.1`.

## Test from macOS

```bash
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "mlx-community/Qwen3.6-27B-OptiQ-4bit",
    "messages": [
      {"role": "user", "content": "Write a one-sentence readiness check."}
    ],
    "max_tokens": 128,
    "temperature": 0.6
  }'
```

## Connect from an OrbStack Container

OrbStack gives you two clean ways for a containerized agent to reach a model server running on macOS.

Default bridge networking:

```text
http://host.docker.internal:8080/v1
```

Host networking:

```text
http://localhost:8080/v1
```

Test bridge networking:

```bash
docker run --rm curlimages/curl:latest \
  -s http://host.docker.internal:8080/v1/models
```

Test host networking:

```bash
docker run --rm --net host curlimages/curl:latest \
  -s http://localhost:8080/v1/models
```

## Configure the Agent Container

Default OrbStack bridge networking:

```bash
OPENAI_BASE_URL=http://host.docker.internal:8080/v1
OPENAI_API_KEY=EMPTY
OPENAI_MODEL=mlx-community/Qwen3.6-27B-OptiQ-4bit
```

Example `docker run`:

```bash
docker run --rm -it \
  -e OPENAI_BASE_URL=http://host.docker.internal:8080/v1 \
  -e OPENAI_API_KEY=EMPTY \
  -e OPENAI_MODEL=mlx-community/Qwen3.6-27B-OptiQ-4bit \
  your-agent-image
```

Host networking variant:

```bash
docker run --rm -it --net host \
  -e OPENAI_BASE_URL=http://localhost:8080/v1 \
  -e OPENAI_API_KEY=EMPTY \
  -e OPENAI_MODEL=mlx-community/Qwen3.6-27B-OptiQ-4bit \
  your-agent-image
```

Compose with default bridge networking:

```yaml
services:
  agent:
    image: your-agent-image
    environment:
      OPENAI_BASE_URL: http://host.docker.internal:8080/v1
      OPENAI_API_KEY: EMPTY
      OPENAI_MODEL: mlx-community/Qwen3.6-27B-OptiQ-4bit
```

Compose with OrbStack host networking:

```yaml
services:
  agent:
    image: your-agent-image
    network_mode: host
    environment:
      OPENAI_BASE_URL: http://localhost:8080/v1
      OPENAI_API_KEY: EMPTY
      OPENAI_MODEL: mlx-community/Qwen3.6-27B-OptiQ-4bit
```

## Optional: Try the MoE Model

After the 27B baseline works, test the 35B-A3B OptiQ model:

```bash
mlx_lm.server \
  --model mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit \
  --host 127.0.0.1 \
  --port 8080
```

Then switch the agent:

```bash
OPENAI_MODEL=mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit
```

Compare:

- First-token latency.
- Tokens per second on long responses.
- Tool-call reliability.
- Memory pressure in Activity Monitor.
- Quality on your actual development tasks.

## Optional: Use MTP / Speculative Decoding

The OptiQ cards for Qwen3.6-27B and Qwen3.6-35B-A3B say they include a bundled `mtp.safetensors` head and can use `mlx-optiq` for speculative decoding.

Install:

```bash
uv tool install mlx-optiq
```

Run:

```bash
optiq serve --model mlx-community/Qwen3.6-27B-OptiQ-4bit --mtp
```

The cards report roughly 1.4x faster decode with MTP. Treat this as a second step after the basic `mlx_lm.server` path works.

## When to Fall Back to llama.cpp

Use llama.cpp if you hit one of these problems with MLX:

- Your agent expects stricter OpenAI-compatible streaming behavior.
- Tool calls come back malformed or hard for the agent to parse.
- You need grammar-constrained JSON output.
- You want the broader GGUF ecosystem and more server flags.

Fallback command:

```bash
brew install llama.cpp

llama-server \
  -hf unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q4_K_M \
  --host 127.0.0.1 \
  --port 8080 \
  -c 32768
```

The agent wiring is the same:

```bash
OPENAI_BASE_URL=http://host.docker.internal:8080/v1
OPENAI_API_KEY=EMPTY
```

## Tuning Notes

Start with conservative generation settings:

```json
{
  "temperature": 0.6,
  "top_p": 0.95,
  "top_k": 20,
  "presence_penalty": 0.0,
  "max_tokens": 4096
}
```

Qwen's model card recommends `temperature=0.6`, `top_p=0.95`, and `top_k=20` for precise coding tasks.

For context length, do not jump straight to Qwen3.6's full 262K native context. Start by testing normal agent loops, then raise only if you need it. Long context consumes KV-cache memory and can quickly dominate a 64 GB machine.

If macOS warns that a large MLX model may be slow, MLX LM documents increasing the wired memory limit with:

```bash
sudo sysctl iogpu.wired_limit_mb=N
```

Choose `N` below total memory and above the model/cache working set. Do not set this blindly; use it only if MLX warns and Activity Monitor shows memory headroom.

## Security Notes

Prefer binding MLX to `127.0.0.1` first. With OrbStack bridge networking, try `host.docker.internal` from the container. If that does not work, either use OrbStack host networking for the agent or bind MLX to `0.0.0.0`.

If you bind MLX or llama.cpp to `0.0.0.0`, assume it may be reachable from other network peers unless the macOS firewall or your network settings block it.

Do not put real API keys in this local model configuration. OpenAI-compatible local servers generally require an API key field for client compatibility, but ignore the value.

## Troubleshooting

Confirm OrbStack is active:

```bash
docker context show
```

Test the server from macOS:

```bash
curl http://localhost:8080/v1/models
```

Test host access from a bridge container:

```bash
docker run --rm curlimages/curl:latest \
  -v http://host.docker.internal:8080/v1/models
```

Test host networking:

```bash
docker run --rm --net host curlimages/curl:latest \
  -v http://localhost:8080/v1/models
```

If `host.docker.internal` fails but `--net host` works, keep the agent on `--net host` or bind the MLX server to `0.0.0.0`.

Out-of-memory or heavy swapping:

1. Try the 27B OptiQ model before 35B-A3B 8-bit.
2. Reduce context size or max output.
3. Close memory-heavy apps.
4. Avoid vision models unless you need image input.

Bad tool calls:

1. Lower temperature.
2. Add explicit JSON/tool-call instructions to the system prompt.
3. Fall back to llama.cpp if the agent requires strict parser behavior.

`urllib3` / LibreSSL warning:

1. Check the launcher with `head -n 1 "$(which mlx_lm.server)"`.
2. If it points at an old Python such as `.../uv/tools/mlx-lm/bin/python`, check that Python with `.../bin/python --version`.
3. Install or pin uv Python 3.14 with `uv python install 3.14` and `uv python pin 3.14`.
4. Reinstall with `uv tool uninstall mlx-lm` and `uv tool install mlx-lm`.
5. If uv still chooses an old interpreter, inspect uv's installed interpreters with `uv python list --only-installed`.

## Sources

- Qwen3.6-27B model card: https://huggingface.co/Qwen/Qwen3.6-27B
- Qwen3.6-35B-A3B model card: https://huggingface.co/Qwen/Qwen3.6-35B-A3B
- Qwen3.6-27B OptiQ MLX quant: https://huggingface.co/mlx-community/Qwen3.6-27B-OptiQ-4bit
- Qwen3.6-35B-A3B OptiQ MLX quant: https://huggingface.co/mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit
- Qwen3.6-27B MLX vision quant: https://huggingface.co/mlx-community/Qwen3.6-27B-4bit
- Qwen3.6-35B-A3B MLX vision quant: https://huggingface.co/mlx-community/Qwen3.6-35B-A3B-4bit
- Unsloth Qwen3.6-35B-A3B MLX 8-bit: https://huggingface.co/unsloth/Qwen3.6-35B-A3B-MLX-8bit
- MLX LM: https://github.com/ml-explore/mlx-lm
- MLX LM PyPI package metadata: https://pypi.org/project/mlx-lm/
- OrbStack container networking: https://docs.orbstack.dev/docker/network
- OrbStack host networking: https://docs.orbstack.dev/docker/host-networking
- llama.cpp README: https://github.com/ggml-org/llama.cpp
