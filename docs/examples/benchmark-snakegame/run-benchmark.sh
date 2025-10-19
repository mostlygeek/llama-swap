#!/usr/bin/env bash

# This script generates a CSV file showing the token/second for generating a Snake Game in python, typescript and swift
# It was created to test the effects of speculative decoding and the various draft settings on performance.
#
# Writing code with a low temperature seems to provide fairly consistent logic.
#
# Usage: ./benchmark.sh <url> <model1> [model2 ...]
# Example: ./benchmark.sh http://localhost:8080 model1 model2

if [ "$#" -lt 2 ]; then
    echo "Usage: $0 <url> <model1> [model2 ...]"
    exit 1
fi

url=$1; shift

echo "model,python,typescript,swift"

for model in "$@"; do

    echo -n "$model,"

    for lang in "python" "typescript" "swift"; do
        # expects a llama.cpp after PR https://github.com/ggerganov/llama.cpp/pull/10548
        # (Dec 3rd/2024)
        time=$(curl -s --url "$url/v1/chat/completions" -d "{\"messages\": [{\"role\": \"system\", \"content\": \"you only write code.\"}, {\"role\": \"user\", \"content\": \"write snake game in $lang\"}], \"top_k\": 1, \"timings_per_token\":true, \"model\":\"$model\"}" | jq -r .timings.predicted_per_second)

        if [ $? -ne 0 ]; then
            time="error"
            exit 1
        fi

        if [ "$lang" != "swift" ]; then
            printf "%0.2f tps," $time
        else
            printf "%0.2f tps\n" $time
        fi
    done
done