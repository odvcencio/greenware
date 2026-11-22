(() => {
  const form = document.getElementById("beacon-recipe");
  if (!form) return;
  const mount = document.querySelector("#beacon-stage [data-gosx-engine]");
  const status = document.getElementById("beacon-status");
  const download = document.getElementById("beacon-download");
  if (!mount || !status || !download) return;

  let timer;
  let pending;
  let revision = 0;
  form.dataset.live = "true";

  function report(message, error = false) {
    status.textContent = message;
    status.dataset.error = String(error);
  }

  function schedule(delay) {
    for (const value of form.querySelectorAll("[data-range-value]")) {
      value.textContent = form.elements[value.dataset.rangeValue].value;
    }
    clearTimeout(timer);
    pending?.abort();
    const request = ++revision;
    if (!form.checkValidity()) {
      report("Enter a seed using digits only.", true);
      return;
    }
    report("Generating beacon…");
    timer = setTimeout(() => generate(request), delay);
  }

  async function generate(request) {
    const query = new URLSearchParams(new FormData(form));
    const controller = new AbortController();
    pending = controller;
    try {
      const response = await fetch("/beacon-assets/recipe?" + query, { signal: controller.signal });
      if (!response.ok) throw new Error((await response.text()).trim());
      const next = await response.json();
      if (request !== revision) return;
      const scene = window.__gosx?.scene3d;
      if (!scene) throw new Error("The 3D scene is still starting.");
      await scene.dispatchCommands(mount, next.commands, { signal: controller.signal });
      if (request !== revision) return;
      download.href = next.source;
      history.replaceState(history.state, "", "/beacon?" + query);
      report(next.payloads + " panel modules · 1 status light");
    } catch (error) {
      if (request === revision && error.name !== "AbortError") {
        report(error.message || "The beacon could not be updated.", true);
      }
    } finally {
      if (pending === controller) pending = undefined;
    }
  }

  form.addEventListener("input", () => schedule(160));
  form.addEventListener("submit", event => {
    event.preventDefault();
    schedule(0);
  });
})();
