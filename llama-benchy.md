# Fork/branch notes for benchy-ui
# origin:   https://github.com/vedcsolution/llama-swap
# upstream: https://github.com/mostlygeek/llama-swap

# 1) Update branch with latest upstream main
cd /home/csolutions_ai/llama-swap
git fetch upstream
git switch benchy-ui
git rebase upstream/main

# 2) Build UI + binary
cd ui-svelte && npm run build
cd ..
docker run --rm -v "$PWD:/work" -w /work golang:1.25 bash -c 'CGO_ENABLED=0 go build -buildvcs=false -o build/llama-swap .'

# 3) Deploy safely (avoids "Text file busy")
sudo systemctl stop llama-swap
sudo cp build/llama-swap /usr/local/bin/llama-swap
sudo systemctl start llama-swap
sudo systemctl is-active llama-swap

# 4) Push rebased branch to your fork
git push --force-with-lease origin benchy-ui
