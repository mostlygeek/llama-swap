<div class="h-full">
  <div class="card max-w-4xl">
    <h2 class="pb-0">Help</h2>

    <p class="text-sm text-txtsecondary mt-2">
      Esta configuración usa como backend de recetas:
    </p>
    <p class="text-sm break-all">
      <a class="underline" href="https://github.com/eugr/spark-vllm-docker" target="_blank" rel="noreferrer">
        eugr/spark-vllm-docker
      </a>
    </p>

    <p class="text-sm text-txtsecondary mt-4">
      Integración de benchmark (Benchy):
    </p>
    <p class="text-sm break-all">
      <a class="underline" href="https://github.com/christopherowen/llama-benchy" target="_blank" rel="noreferrer">
        christopherowen/llama-benchy
      </a>
    </p>

    <p class="text-sm text-txtsecondary mt-4">
      Créditos:
      <span class="font-mono text-txtmain">@eugr</span>,
      <span class="font-mono text-txtmain">@christopherowen</span>,
      <span class="font-mono text-txtmain">@raphael.amorim</span>
    </p>

    <p class="text-xs text-txtsecondary mt-4">
      Tip: puedes sobreescribir rutas con variables de entorno como
      <code class="font-mono">LLAMA_SWAP_RECIPES_BACKEND_DIR</code> y
      <code class="font-mono">LLAMA_SWAP_LOCAL_RECIPES_DIR</code>.
    </p>

    <p class="text-sm text-txtsecondary mt-5">
      NVMe-oF Canary Toolkit:
    </p>
    <p class="text-xs text-txtsecondary mt-1">
      Este fork incluye scripts para hardening canario y reconnect seguro:
    </p>
    <pre class="mt-1 text-xs font-mono bg-card/60 border border-card-border rounded p-2 overflow-x-auto"><code>scripts/nvmeof-initiator-canary.sh
scripts/net-tune-canary.sh
scripts/install-nvmeof-canary-units.sh</code></pre>
    <p class="text-xs text-txtsecondary mt-2">
      Plantillas systemd:
      <code class="font-mono">scripts/systemd/nvmeof-connect@.service</code> y
      <code class="font-mono">scripts/systemd/net-tune-canary.service</code>.
    </p>
    <p class="text-xs text-txtsecondary mt-2">
      En <strong>Cluster</strong> se muestra un baseline de almacenamiento actual por nodo para visualizar
      rutas duplicadas; no cambia tus rutas actuales, solo ayuda a priorizar migración a lectura compartida.
    </p>

    <p class="text-sm text-txtsecondary mt-5">
      Troubleshooting (Intelligence / SWE-bench):
    </p>
    <p class="text-xs text-txtsecondary mt-1">
      Si aparece <code class="font-mono">PermissionError</code> con locks en
      <code class="font-mono">~/.cache/huggingface/datasets</code>, corrige permisos del cache existente:
    </p>
    <pre class="mt-1 text-xs font-mono bg-card/60 border border-card-border rounded p-2 overflow-x-auto"><code>sudo chown -R $USER:$USER ~/.cache/huggingface/datasets</code></pre>
    <p class="text-xs text-txtsecondary mt-2">
      Prueba rápida de carga para validar que el lock no falla:
    </p>
    <pre class="mt-1 text-xs font-mono bg-card/60 border border-card-border rounded p-2 overflow-x-auto"><code>uvx --from "llama-benchy[intelligence] @ git+https://github.com/christopherowen/llama-benchy.git@intelligence" \
python -c "from datasets import load_dataset; ds=load_dataset('SWE-bench/SWE-bench_Verified', split='test[:1]'); print('ok', len(ds))"</code></pre>
  </div>
</div>
