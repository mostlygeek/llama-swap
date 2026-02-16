#!/usr/bin/env python3
"""Patch vLLM 0.15 for GLM-4.7-Flash on DGX Spark (GB10, sm_121).

Fix 0: torch._inductor.config.assume_32bit_indexing missing in PyTorch 2.10
       → monkey-patch so torch.compile/cudagraphs work (no --enforce-eager needed)

Fix 1: Anthropic streaming: content="" and tool_calls=[...] in same chunk
       → tool_calls never processed. Check tool_calls before content.

Fix 2: Anthropic streaming: tool_call id+name+args in single chunk (glm47)
       → input_json_delta never emitted. Emit args when present with id.
"""
import os

# === Fix 0: torch._inductor.config.assume_32bit_indexing ===
# NVIDIA PyTorch 2.10.0a0 reports as 2.10.0 but lacks this config key.
# vLLM's version check passes, then crashes in child processes.
# Fix: Add the attribute to config.py on disk so all processes see it.
_ind_cfg_path = '/usr/local/lib/python3.12/dist-packages/torch/_inductor/config.py'
if os.path.exists(_ind_cfg_path):
    with open(_ind_cfg_path) as f:
        _ind_src = f.read()
    if 'assume_32bit_indexing' not in _ind_src:
        _ind_src = _ind_src.replace(
            'install_config_module(sys.modules[__name__])',
            '# Added by patch: missing in NVIDIA PyTorch 2.10.0a0\n'
            'assume_32bit_indexing = True\n\n'
            'install_config_module(sys.modules[__name__])'
        )
        with open(_ind_cfg_path, 'w') as f:
            f.write(_ind_src)
        # Clear .pyc
        for d, _, files in os.walk(os.path.dirname(_ind_cfg_path)):
            for fn in files:
                if fn.endswith('.pyc'):
                    os.remove(os.path.join(d, fn))
        print('Fix 0: assume_32bit_indexing added to torch._inductor.config')
    else:
        print('Fix 0: Already present')
else:
    print('Fix 0: SKIP - config.py not found')

fpath = '/usr/local/lib/python3.12/dist-packages/vllm/entrypoints/anthropic/serving.py'
with open(fpath) as f:
    content = f.read()

patched = False

# === Fix 1: tool_calls prioritized over empty content ===
if '_has_tc' not in content:
    lines = content.split('\n')
    content_check_line = None
    tool_calls_elif_line = None
    for i, line in enumerate(lines):
        stripped = line.strip()
        if stripped == 'if origin_chunk.choices[0].delta.content is not None:':
            content_check_line = i
        if stripped == 'elif len(origin_chunk.choices[0].delta.tool_calls) > 0:':
            tool_calls_elif_line = i

    if content_check_line is not None and tool_calls_elif_line is not None:
        indent = lines[content_check_line][:len(lines[content_check_line]) - len(lines[content_check_line].lstrip())]
        indent2 = lines[tool_calls_elif_line][:len(lines[tool_calls_elif_line]) - len(lines[tool_calls_elif_line].lstrip())]

        lines[content_check_line] = (
            f"{indent}_has_tc = (origin_chunk.choices[0].delta.tool_calls\n"
            f"{indent}           and len(origin_chunk.choices[0].delta.tool_calls) > 0)\n"
            f"{indent}if origin_chunk.choices[0].delta.content is not None and not _has_tc:"
        )
        lines[tool_calls_elif_line] = f"{indent2}elif _has_tc:"

        content = '\n'.join(lines)
        patched = True
        print('Fix 1: tool_calls prioritized over empty content')
    else:
        print(f'Fix 1: SKIP - pattern not found (content_check={content_check_line}, elif={tool_calls_elif_line})')
else:
    print('Fix 1: Already applied')

# === Fix 2: emit input_json_delta when arguments in same chunk as id ===
# Find the pattern where content_block_start is yielded for tool_use,
# and add input_json_delta emission when arguments are present.
old_tool_start = '''                                data = chunk.model_dump_json(exclude_unset=True)
                                yield wrap_data_with_event(data, "content_block_start")
                                content_block_started = True

                            else:
                                chunk = AnthropicStreamEvent(
                                    index=content_block_index,
                                    type="content_block_delta",
                                    delta=AnthropicDelta(
                                        type="input_json_delta",
                                        partial_json=tool_call.function.arguments
                                        if tool_call.function
                                        else None,'''

new_tool_start = '''                                data = chunk.model_dump_json(exclude_unset=True)
                                yield wrap_data_with_event(data, "content_block_start")
                                content_block_started = True

                                # glm47 parser sends id+name+args in one chunk
                                if (tool_call.function
                                        and tool_call.function.arguments):
                                    _args_chunk = AnthropicStreamEvent(
                                        index=content_block_index,
                                        type="content_block_delta",
                                        delta=AnthropicDelta(
                                            type="input_json_delta",
                                            partial_json=tool_call.function.arguments,
                                        ),
                                    )
                                    _args_data = _args_chunk.model_dump_json(
                                        exclude_unset=True)
                                    yield wrap_data_with_event(
                                        _args_data, "content_block_delta")

                            else:
                                chunk = AnthropicStreamEvent(
                                    index=content_block_index,
                                    type="content_block_delta",
                                    delta=AnthropicDelta(
                                        type="input_json_delta",
                                        partial_json=tool_call.function.arguments
                                        if tool_call.function
                                        else None,'''

if old_tool_start in content:
    content = content.replace(old_tool_start, new_tool_start, 1)
    patched = True
    print('Fix 2: input_json_delta emitted for single-chunk tool calls')
elif 'glm47 parser sends' in content:
    print('Fix 2: Already applied')
else:
    print('Fix 2: SKIP - pattern not found')

if patched:
    with open(fpath, 'w') as f:
        f.write(content)
    # Clear .pyc
    for d, _, files in os.walk('/usr/local/lib/python3.12/dist-packages/vllm/entrypoints/anthropic'):
        for fn in files:
            if fn.endswith('.pyc'):
                os.remove(os.path.join(d, fn))
    print('PATCHED successfully!')
else:
    print('No patches applied')
