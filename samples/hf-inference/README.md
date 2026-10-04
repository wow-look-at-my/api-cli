# Hugging Face Inference Endpoints

`hf-inference.xml` wraps the Hugging Face Inference Endpoints API, version 2. That is the API behind `hf endpoints`. `api-cli` makes the HTTP requests itself, so you install nothing else -- no Python, no `huggingface_hub`.

Every call needs an access token in `$HF_TOKEN`. `$HUGGING_FACE_HUB_TOKEN` also works. The first argument of each command is the namespace, which is a user or an organization.

```sh
api-cli --config samples/hf-inference/hf-inference.xml list my-org
api-cli --config samples/hf-inference/hf-inference.xml get my-org my-endpoint
api-cli --config samples/hf-inference/hf-inference.xml pause my-org my-endpoint
api-cli --config samples/hf-inference/hf-inference.xml resume my-org my-endpoint
api-cli --config samples/hf-inference/hf-inference.xml scale-to-zero my-org my-endpoint
api-cli --config samples/hf-inference/hf-inference.xml logs my-org my-endpoint --tail 100
```

`create` takes the hardware as flags. `hf endpoints hardware` lists the valid combinations. `--type` has no default, so each new endpoint states its access level. The replica flags default to a minimum of 0 and a 15 minute scale-to-zero timeout. As a result, an idle endpoint stops billing.

```sh
api-cli --config samples/hf-inference/hf-inference.xml create my-org my-endpoint \
	--repository Qwen/Qwen2.5-0.5B-Instruct --framework pytorch --task text-generation \
	--accelerator gpu --instance-type nvidia-l4 --instance-size x1 \
	--vendor aws --region us-east-1 --type authenticated
```

`update` sends only the fields its flags set. The API keeps the rest of the endpoint as it is. `delete` asks for confirmation, and `--yes` skips the prompt.

The same config is an MCP server. Each command becomes a tool.

```sh
HF_TOKEN=hf_xxx api-cli --config samples/hf-inference/hf-inference.xml --mcp stdio
```

`$HF_ENDPOINTS_API_URL` points the config at another base URL. The tests use it to aim the requests at a local stub.
