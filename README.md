# model-intake

Vet Hugging Face models before they enter your environment.

`model-intake` is a Go command-line tool that reviews a Hugging Face model repository before anyone downloads or loads it. It flags pickle-based weight files that can execute code on load, checks the publisher and license against your policy, records the exact commit you reviewed, and writes a report your CI pipeline can act on.

It is a static scanner. It reads metadata and raw bytes, and it never loads or runs a model.

## Why

Pickle-based model files (`.bin`, `.pt`, `.pth`, `.ckpt`, `.pkl`) can run arbitrary code the moment they are loaded. Hugging Face scans uploads, but its own documentation says the scanning is not foolproof and leaves the final safety call to the user. Teams in regulated environments need an intake step that is repeatable, policy-driven, and leaves an audit trail.

Most files hold data. A pickle file holds instructions. When Python opens one, it doesn't just read it. It follows the instructions inside to rebuild whatever was saved, and one of the things those instructions can say is "run this function."

PyTorch uses that for an ordinary reason: rebuilding the model's weights. But nothing stops a file from asking Python to run something else instead, like a command that installs malware or sends your passwords somewhere. The format itself can't tell a normal model from a malicious one.

A simple way to think about it: opening a plain data file is like reading a spreadsheet. Opening a pickle file is like running a macro someone sent you.

Why this matters in practice:

- Loading is enough. You don't have to use the model for anything. The moment it loads, any hidden code runs with the same access you have: your files, your network, and any passwords or cloud keys on that machine.
- Common file types are affected. .bin, .pt, .pth, .ckpt, .pkl, and .joblib files can all carry pickle data, and many models on Hugging Face still ship their weights this way.
- Built-in protections have had holes. PyTorch now loads files in a safer mode by default, but in 2025 researchers showed that mode could be bypassed in older versions (CVE-2025-32434, rated critical). Older code and other libraries may not use that protection at all.
- Scanners have been fooled. In early 2025, researchers found malicious models on Hugging Face that slipped past its scanner by using deliberately damaged files (nullifAI). The harmful part sat at the very start of the file, so it ran before anything noticed the damage.

Why safetensors is safer

Safetensors was designed so that loading a file never involves running anything. The whole file is three parts:

| 8 bytes: N (uint64, little-endian) | N bytes: JSON header | tensor data buffer |

The header maps each tensor name to its type, shape, and location in the buffer:

json
```
{
  "wte.weight": { "dtype": "F32", "shape": [50257, 768], "data_offsets": [0, 154389504] },
  "__metadata__": { "format": "pt" }
}
```

Loading means parsing that JSON and slicing bytes out of the buffer. There are no imports, no callables, and no object graph to rebuild. The specification adds guardrails that make the file checkable with arithmetic before any tensor is touched:

Header size is capped at 100 MB, so a hostile header cannot exhaust memory during parsing.
Offsets cannot overlap, and the buffer cannot have holes. Every byte belongs to exactly one tensor, which leaves no room to hide extra data.
Sizes must agree. Each tensor's byte range has to equal the product of its shape times the size of its dtype.
Metadata is strings only. __metadata__ is a flat string-to-string map, not arbitrary JSON.
Fixed memory layout. Little-endian, row-major, no striding, which also allows memory-mapped, zero-copy loading.

Safetensors makes loading safe. It does not make the model safe:

- Weights can still be backdoored. That is a behavioral problem no file format can solve.
- A repository can ship both .safetensors and .bin files, and some loaders fall back to the pickle file.
- A repository can ship its own Python code that runs when a user sets trust_remote_code=True, no matter what format the weights are in.
- Any parser can have bugs. That is why formats like ONNX and GGUF land in this tool's "needs review" bucket rather than "safe."

This is why model-intake looks at the whole repository, its publisher, and the exact commit, not only the file extensions.

## What it checks

| Check | What it looks for |
|---|---|
| Serialization format | Pickle-based files vs safetensors, plus formats that need manual review |
| Pickle imports | Dangerous imports inside pickle files, such as `os.system` or `builtins.eval` (planned) |
| Publisher | Author or organization against an allowlist |
| License | Declared license against an allowlist |
| Revision | The commit SHA reviewed, and whether the model has moved since you approved it |
| Access | Whether the repository is gated or private |

## Usage

Review one model by hand:

```sh
  model-intake scan openai-community/gpt2
  model-intake scan openai-community/gpt2 --revision 607a30d --format json
```

Or commit a config file and let CI run it with no arguments:

```sh
model-intake scan                   # reads .model-intake.yaml
model-intake scan --format sarif
```

Exit codes: `0` pass, `1` policy failure, `2` error.

### Example `.model-intake.yaml`

```yaml
version: 1
models:
  - id: openai-community/gpt2
    revision: 607a30d1f6b17a1a9e6b1b3b7a0a4b3e5c2d1f00   # the commit you approved
  - id: google/flan-t5-base
policy:
  require_safetensors: true
  allowed_licenses: [apache-2.0, mit]
  allowed_publishers: [google, microsoft, openai-community]
  require_hf_scan_safe: true # Validates whether Hugging Face Scanners marked the model as safe
  max_age_days: 365 # Checking for abandoned models
  allow_revision_drift: false # Validates whether or not the commit is most recent
```

Commit that file next to your code. Your pipeline then runs one command, and the file doubles as a record of which models were approved and at which commit.

## Roadmap

- [ ] v0.1: Fetch model metadata from the Hub API and classify files by extension
- [ ] v0.2: JSON output, `--revision` pinning, CI-friendly exit codes
- [ ] v0.3: `.model-intake.yaml` for declared models, pinned commits, and policy
- [ ] v0.4: Download pickle files and inspect their imports without executing them
- [ ] v0.5: SARIF output and example GitHub Actions and GitLab CI jobs

## Limitations

- Extension checks are a first pass. A file can be mislabeled.
- A clean scan is not proof of safety. Pickle scanners can be evaded, so prefer safetensors and load untrusted models in a sandbox.
- Does not evaluate model behavior such as bias, jailbreaks, or backdoored weights. That requires running the model. See "Where this fits" below.

## Related tools

Static scanners, the same lane as this tool: [picklescan](https://github.com/mmaitre314/picklescan), [ModelScan](https://github.com/protectai/modelscan) by Protect AI, and [Fickling](https://github.com/trailofbits/fickling) by Trail of Bits. `model-intake` does not replace them. It focuses on a single static binary and policy-driven intake decisions.

Behavioral red-teaming, a different lane: [garak](https://github.com/NVIDIA/garak) by NVIDIA probes a live model for prompt injection, jailbreaks, data leakage, and toxicity.

## Where this fits

`model-intake` is the gate at the door. It is static and fast, so it can run on every commit and block a model before anyone downloads it. Behavioral tools like garak come afterward, against a model that already passed intake, in an isolated environment. The two are sequential, not competing.

## License

Apache-2.0