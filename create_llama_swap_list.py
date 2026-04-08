import argparse
import re

import requests
import yaml

parser = argparse.ArgumentParser()
parser.add_argument('--lazy_config_path', type=str, required=True)
args = parser.parse_args()

with open(args.lazy_config_path, 'r') as f:
	data = yaml.safe_load(f)

proxy = data.get('proxy', 'http://127.0.0.1:${PORT}')
commands = data.get('commands', {}) or {}
quants = data.get('quant', {}) or {}
models = data.get('models', []) or []


def get_specific_quants(repo_id):
	# Hugging Face API URL for model info
	api_url = f'https://huggingface.co/api/models/{repo_id}'

	try:
		response = requests.get(api_url)
		response.raise_for_status()
		data = response.json()

		# 'siblings' contains the list of all files in the repo
		files = [f['rfilename'] for f in data.get('siblings', [])]

		# Regex to find patterns like Q4_K_M, IQ4_NL, Q8_0, bf16, etc.
		# This looks for:
		# 1. Q or IQ followed by a digit (Q4, IQ3)
		# 2. Optional underscores and letters (K_M, NL, S, XS)
		quant_pattern = r'(I?Q\d_[A-Z0-9_]+|I?Q\d_[0-9]|F16|F32|BF16)'

		found_quants = set()

		for file in files:
			if file.endswith('.gguf'):
				# Search for the quant pattern in the filename
				match = re.search(quant_pattern, file, re.IGNORECASE)
				if match:
					found_quants.add(match.group(1).upper())
				# Special case: if regex misses simple ones like Q4_0
				elif 'Q' in file.upper():
					# Fallback logic to grab string between dots/dashes
					parts = re.split(r'[\.\-_]', file)
					for p in parts:
						if p.upper().startswith('Q') and any(
							char.isdigit() for char in p
						):
							found_quants.add(p.upper())

		return sorted(list(found_quants))

	except Exception as e:
		return f'Error: {e}'


def extend_model(model, quants_name_list):
	if quants_name_list:
		return [f'{model}:{quant}' for quant in quants_name_list]
	else:
		return [f'{model}:{quant}' for quant in get_specific_quants(model)] or [model]


def create_command(model):
	for model_type, command in commands.items():
		if model_type.lower() in model.lower():
			command = commands[model_type]
			model_commands = []
			for model_name in extend_model(model, quants.get(model_type, [])):
				model_commands.append({model_name: {'proxy': proxy, 'cmd': command.replace('${model}', f'"{model_name}"')}})
			return model_commands

	command = commands.get('defualt', 'llama-server -hf ${model} --port ${PORT}')
	command = command.replace('${model}', f'"{model}"')
	return [{model: {'proxy': proxy, 'cmd': command}}]


creating_yaml = {'models': []}
models_list = []

for model in models:
	models_list.extend(create_command(model))

creating_yaml['models'] = models_list

with open('swap_list.yaml', 'w') as f:
	yaml.dump(creating_yaml, f)
