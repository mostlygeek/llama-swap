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
        response=$(curl -s --url "$url/v1/chat/completions" -d "{\"messages\": [{\"role\": \"system\", \"content\": \"you only write code.\"}, {\"role\": \"user\", \"content\": \"write snake game in $lang\"}], \"temperature\": 0.1, \"model\":\"$model\"}")
        if [ $? -ne 0 ]; then
            time="error"
        else
            time=$(curl -s --url "$url/logs" | grep -oE '\d+(?:\.\d+)? tokens per second' | awk '{print $1}' | tail -n 1)
            if [ $? -ne 0 ]; then
                time="error"
            fi
        fi

        if [ "$lang" != "swift" ]; then
            echo -n "$time,"
        else
            echo -n "$time"
        fi
    done

    echo ""
done
