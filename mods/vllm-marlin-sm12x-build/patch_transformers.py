#!/usr/bin/env python3
"""Patch transformers 5.0 for vLLM compatibility (vllm-next).

Patches:
  Patch 3: compressed_tensors extra='forbid' → 'ignore'
  Patch 4: find_matched_target MoE fallback for non-standard layer names
  Patch 5: Clear .pyc caches
  Patch 7: AutoWeightsLoader named_buffers support
  Patch 8: MoE ignore e_score_correction_bias + MTP layers
  Patch 11: Anthropic streaming adapter tool_calls fix

NOTE: Patch 10 (CUTLASS group_gemm SM120+ disable) is INTENTIONALLY REMOVED.
      vllm-next uses CUTLASS 4.3.5 compiled for SM120a/SM121a.
"""
import subprocess
import os

# Find ALL vllm installations
vllm_roots = []
for root in ['/usr/local/lib/python3.12/dist-packages/vllm',
             '/opt/vllm/vllm-src/vllm']:
    if os.path.isdir(root):
        vllm_roots.append(root)
print(f'Found vllm installations: {vllm_roots}')

# ============================================================
# Patch 3: compressed_tensors QuantizationArgs extra='forbid' → 'ignore'
# transformers 5.0 adds scale_dtype/zp_dtype to quantization config
# but compressed_tensors uses extra='forbid' and rejects them
# ============================================================
ct_base = '/usr/local/lib/python3.12/dist-packages/compressed_tensors'
if os.path.isdir(ct_base):
    patched_files = []
    for dirpath, dirnames, filenames in os.walk(ct_base):
        for fname in filenames:
            if fname.endswith('.py'):
                fpath = os.path.join(dirpath, fname)
                with open(fpath) as f:
                    fc = f.read()
                if "extra='forbid'" in fc or 'extra="forbid"' in fc:
                    fc = fc.replace("extra='forbid'", "extra='ignore'")
                    fc = fc.replace('extra="forbid"', 'extra="ignore"')
                    with open(fpath, 'w') as f:
                        f.write(fc)
                    relpath = os.path.relpath(fpath, ct_base)
                    patched_files.append(relpath)
    if patched_files:
        print(f'Patch 3: compressed_tensors extra=ignore in: {", ".join(patched_files)}')
    else:
        print('Patch 3: No extra=forbid found (already patched or not needed)')

# ============================================================
# Patch 4: find_matched_target MoE fallback
# MoE expert layers and non-standard projection names (e.g., q_a_proj,
# kv_a_proj_with_mqa in GLM-4.7-Flash) need a generic Linear fallback
# ============================================================
for root in vllm_roots:
    utils_path = os.path.join(
        root, 'model_executor/layers/quantization/compressed_tensors/utils.py')
    if os.path.exists(utils_path):
        with open(utils_path) as f:
            content = f.read()

        # Pattern for the ValueError raise (robust matching)
        old_raise = '''    if matched_target is None:
        raise ValueError(
            f"Unable to find matching target for {layer_name} in the "
            "compressed-tensors config."
        )'''

        new_raise = '''    # Generic Linear fallback: if "Linear" is in targets and no match found,
    # match it. Any layer reaching here through get_quant_method IS a linear
    # layer (checked via isinstance(layer, LinearBase) or FusedMoE).
    # This handles MoE expert layers and non-standard projection names
    # (e.g., q_a_proj, kv_a_proj_with_mqa in GLM-4.7-Flash).
    if matched_target is None:
        for t in targets:
            if t == "Linear" or t.endswith("Linear"):
                matched_target = t
                break

    if matched_target is None:
        raise ValueError(
            f"Unable to find matching target for {layer_name} in the "
            "compressed-tensors config."
        )'''

        if old_raise in content and 'MoE fallback' not in content:
            content = content.replace(old_raise, new_raise)
            with open(utils_path, 'w') as f:
                f.write(content)
            print(f'Patch 4: MoE fallback in {utils_path}')
        elif 'MoE fallback' in content:
            print(f'Patch 4: Already patched in {utils_path}')
        else:
            print(f'Patch 4: Could not find exact pattern in {utils_path}')

# ============================================================
# Patch 7: AutoWeightsLoader named_buffers support
# The original only handles BatchNorm statistics, but models may have
# registered buffers (e.g., e_score_correction_bias in GLM-4.7 MoE gate)
# ============================================================
for root in vllm_roots:
    utils_path = os.path.join(root, 'model_executor/models/utils.py')
    if os.path.exists(utils_path):
        with open(utils_path) as f:
            content = f.read()

        old_method = '''    def _add_loadable_non_param_tensors(
        self, module: nn.Module, child_params: dict[str, torch.Tensor]
    ):
        """
        Add tensor names that are not in the model params that may be in the
        safetensors, e.g., batch normalization stats.
        """
        if isinstance(
            module,
            (
                nn.BatchNorm1d,
                nn.BatchNorm2d,
                nn.BatchNorm3d,
                nn.LazyBatchNorm1d,
                nn.LazyBatchNorm2d,
                nn.LazyBatchNorm3d,
                nn.SyncBatchNorm,
            ),
        ):
            module_state_dict = module.state_dict()
            for stat_name in ("running_mean", "running_var", "num_batches_tracked"):
                child_params[stat_name] = module_state_dict[stat_name]'''

        new_method = '''    def _add_loadable_non_param_tensors(
        self, module: nn.Module, child_params: dict[str, torch.Tensor]
    ):
        """
        Add tensor names that are not in the model params that may be in the
        safetensors, e.g., batch normalization stats or registered buffers.
        """
        # Add all registered buffers (e.g., e_score_correction_bias in MoE gates)
        for buf_name, buf in module.named_buffers(recurse=False):
            if buf_name not in child_params:
                child_params[buf_name] = buf
        if isinstance(
            module,
            (
                nn.BatchNorm1d,
                nn.BatchNorm2d,
                nn.BatchNorm3d,
                nn.LazyBatchNorm1d,
                nn.LazyBatchNorm2d,
                nn.LazyBatchNorm3d,
                nn.SyncBatchNorm,
            ),
        ):
            module_state_dict = module.state_dict()
            for stat_name in ("running_mean", "running_var", "num_batches_tracked"):
                child_params[stat_name] = module_state_dict[stat_name]'''

        if old_method in content and 'named_buffers' not in content.split(
                '_add_loadable_non_param_tensors')[1].split('def ')[0][:200]:
            content = content.replace(old_method, new_method)
            with open(utils_path, 'w') as f:
                f.write(content)
            print(f'Patch 7: AutoWeightsLoader buffer support in {utils_path}')
        elif 'named_buffers' in content:
            print(f'Patch 7: Already patched in {utils_path}')
        else:
            print(f'Patch 7: Could not find pattern in {utils_path}')

# ============================================================
# Patch 8: MoE ignore e_score_correction_bias + MTP layers
# a) vLLM replaces MoE gate with TransformersFusedMoE which doesn't
#    register e_score_correction_bias buffer → ignore it during loading
# b) GLM-4.7-Flash has num_nextn_predict_layers=1 (MTP layer at idx 47)
#    not used at inference but has weights in checkpoint → ignore
# ============================================================
for root in vllm_roots:
    moe_mixin_path = os.path.join(
        root, 'model_executor/models/transformers/moe.py')
    if os.path.exists(moe_mixin_path):
        with open(moe_mixin_path) as f:
            content = f.read()

        if 'ignore_unexpected_suffixes.append("e_score_correction_bias")' not in content:
            # vLLM 0.15 pattern (with check_version call)
            old_init_015 = (
                '        self.check_version("5.0.0.dev0", "MoE models support")\n'
                '        super(MixtureOfExperts, self).__init__'
                '(vllm_config=vllm_config, prefix=prefix)'
            )
            new_init_015 = (
                '        self.check_version("5.0.0.dev0", "MoE models support")\n'
                '        super(MixtureOfExperts, self).__init__'
                '(vllm_config=vllm_config, prefix=prefix)\n'
                '        # GLM-4.7-Flash has e_score_correction_bias in MoE gate,\n'
                '        # but vLLM does not use it (use_grouped_topk=False)\n'
                '        self.ignore_unexpected_suffixes.append('
                '"e_score_correction_bias")\n'
                '        # Ignore MTP (Multi-Token Prediction) layers not used at inference\n'
                '        _num_nextn = getattr(self.text_config, "num_nextn_predict_layers", 0)\n'
                '        if _num_nextn > 0:\n'
                '            _num_hidden = self.text_config.num_hidden_layers\n'
                '            for _i in range(_num_nextn):\n'
                '                self.ignore_unexpected_prefixes.append('
                'f"model.layers.{_num_hidden + _i}")'
            )

            # vLLM 0.12 pattern (without check_version call)
            old_init_012 = (
                '        super(MixtureOfExperts, self).__init__'
                '(vllm_config=vllm_config, prefix=prefix)'
            )
            new_init_012 = (
                '        super(MixtureOfExperts, self).__init__'
                '(vllm_config=vllm_config, prefix=prefix)\n'
                '        # GLM-4.7-Flash has e_score_correction_bias in MoE gate,\n'
                '        # but vLLM does not use it (use_grouped_topk=False)\n'
                '        self.ignore_unexpected_suffixes.append('
                '"e_score_correction_bias")\n'
                '        # Ignore MTP (Multi-Token Prediction) layers not used at inference\n'
                '        _num_nextn = getattr(self.text_config, "num_nextn_predict_layers", 0)\n'
                '        if _num_nextn > 0:\n'
                '            _num_hidden = self.text_config.num_hidden_layers\n'
                '            for _i in range(_num_nextn):\n'
                '                self.ignore_unexpected_prefixes.append('
                'f"model.layers.{_num_hidden + _i}")'
            )

            if old_init_015 in content:
                content = content.replace(old_init_015, new_init_015)
                with open(moe_mixin_path, 'w') as f:
                    f.write(content)
                print(f'Patch 8: e_score_correction_bias + MTP ignore in {moe_mixin_path} (0.15 pattern)')
            elif old_init_012 in content:
                content = content.replace(old_init_012, new_init_012)
                with open(moe_mixin_path, 'w') as f:
                    f.write(content)
                print(f'Patch 8: e_score_correction_bias + MTP ignore in {moe_mixin_path} (0.12 pattern)')
            else:
                print(f'Patch 8: Could not find MoEMixin init pattern in {moe_mixin_path}')
        else:
            print(f'Patch 8: Already patched in {moe_mixin_path}')

# NOTE: Patch 10 (CUTLASS group_gemm SM120+ disable) REMOVED for vllm-next.
# CUTLASS 4.3.5 with TORCH_CUDA_ARCH_LIST="12.0a;12.1a" compiles
# group_gemm kernels for SM120/SM121 natively.

# ============================================================
# Patch 11: Anthropic streaming adapter - tool_calls dropped
# vLLM's Anthropic streaming adapter checks content before tool_calls.
# When content="" and tool_calls=[...] in the same chunk (glm47 parser),
# the empty content check does 'continue' and tool_calls are never sent.
# Fix: Check for tool_calls before processing empty content.
# ============================================================
for root in vllm_roots:
    serving_path = os.path.join(root, 'entrypoints/anthropic/serving.py')
    if os.path.exists(serving_path):
        with open(serving_path) as f:
            lines = f.readlines()

        content_check_line = None
        tool_calls_elif_line = None
        for i, line in enumerate(lines):
            stripped = line.strip()
            if stripped == 'if origin_chunk.choices[0].delta.content is not None:':
                content_check_line = i
            if stripped == 'elif len(origin_chunk.choices[0].delta.tool_calls) > 0:':
                tool_calls_elif_line = i

        if '_has_tc' in ''.join(lines):
            print(f'Patch 11: Already patched in {serving_path}')
        elif content_check_line is not None and tool_calls_elif_line is not None:
            indent = lines[content_check_line][:len(lines[content_check_line]) - len(lines[content_check_line].lstrip())]
            indent2 = lines[tool_calls_elif_line][:len(lines[tool_calls_elif_line]) - len(lines[tool_calls_elif_line].lstrip())]

            lines[content_check_line] = (
                f"{indent}_has_tc = (origin_chunk.choices[0].delta.tool_calls\n"
                f"{indent}           and len(origin_chunk.choices[0].delta.tool_calls) > 0)\n"
                f"{indent}if origin_chunk.choices[0].delta.content is not None and not _has_tc:\n"
            )
            lines[tool_calls_elif_line] = f"{indent2}elif _has_tc:\n"

            with open(serving_path, 'w') as f:
                f.writelines(lines)
            print(f'Patch 11: Anthropic streaming tool_calls fix in {serving_path}')
        else:
            print(f'Patch 11: Could not find pattern in {serving_path}')

# ============================================================
# Patch 5: Clear ALL .pyc caches
# ============================================================
for root in vllm_roots + [ct_base]:
    if os.path.isdir(root):
        subprocess.run(['find', root, '-name', '*.pyc', '-delete'])
        subprocess.run(['find', root, '-name', '__pycache__', '-type', 'd',
                         '-exec', 'rm', '-rf', '{}', '+'], capture_output=True)
subprocess.run(['find', '/usr/local/lib/python3.12/dist-packages/transformers',
                '-name', '*.pyc', '-delete'])
subprocess.run(['find', '/usr/local/lib/python3.12/dist-packages/transformers',
                '-name', '__pycache__', '-type', 'd',
                '-exec', 'rm', '-rf', '{}', '+'], capture_output=True)
print('Patch 5: .pyc caches cleared (all locations)')
