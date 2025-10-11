The rerank-test.json data is from https://github.com/ggerganov/llama.cpp/pull/9510

To run it:
> curl http://127.0.0.1:8080/v1/rerank -H "Content-Type: application/json" -d @reranker-test.json  -v | jq .