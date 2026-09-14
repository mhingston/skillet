(() => {
  "use strict";

  const minSidebar = 210;
  const maxSidebar = 420;
  let resizeState = null;

  function setNavigationOpen(open) {
    document.body.classList.toggle("nav-open", open);
    const toggle = document.querySelector("[data-nav-toggle]");
    if (toggle) {
      toggle.setAttribute("aria-expanded", String(open));
      toggle.setAttribute("aria-label", open ? "Close navigation" : "Open navigation");
    }
  }

  function setSidebarWidth(width) {
    const clamped = Math.max(minSidebar, Math.min(maxSidebar, Math.round(width)));
    document.documentElement.style.setProperty("--sidebar-width", `${clamped}px`);
    return clamped;
  }

  document.addEventListener("click", async (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;

    if (target.closest("[data-nav-toggle]")) {
      setNavigationOpen(!document.body.classList.contains("nav-open"));
      return;
    }
    if (target.closest("[data-nav-close]")) {
      setNavigationOpen(false);
      return;
    }
    if (target.closest("#sidebar a") && document.body.classList.contains("nav-open")) {
      setNavigationOpen(false);
    }

    const copyButton = target.closest("[data-copy-target]");
    if (!copyButton) return;
    const id = copyButton.getAttribute("data-copy-target");
    const source = id ? document.getElementById(id) : null;
    if (!source || !navigator.clipboard) return;
    const original = copyButton.textContent;
    try {
      await navigator.clipboard.writeText(source.textContent.trim());
      copyButton.textContent = "Copied";
      window.setTimeout(() => { copyButton.textContent = original; }, 1400);
    } catch (_) {
      copyButton.textContent = "Copy failed";
      window.setTimeout(() => { copyButton.textContent = original; }, 1400);
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && document.body.classList.contains("nav-open")) {
      setNavigationOpen(false);
      const toggle = document.querySelector("[data-nav-toggle]");
      if (toggle) toggle.focus();
      return;
    }

    const handle = event.target instanceof Element ? event.target.closest("[data-sidebar-resize]") : null;
    if (!handle) return;
    const sidebar = document.querySelector("[data-sidebar]");
    if (!sidebar) return;
    const current = sidebar.getBoundingClientRect().width;
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setSidebarWidth(current - 16);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setSidebarWidth(current + 16);
    } else if (event.key === "Home") {
      event.preventDefault();
      setSidebarWidth(minSidebar);
    } else if (event.key === "End") {
      event.preventDefault();
      setSidebarWidth(maxSidebar);
    }
  });

  document.addEventListener("pointerdown", (event) => {
    const handle = event.target instanceof Element ? event.target.closest("[data-sidebar-resize]") : null;
    if (!handle || window.matchMedia("(max-width: 720px)").matches) return;
    const sidebar = document.querySelector("[data-sidebar]");
    if (!sidebar) return;
    resizeState = { startX: event.clientX, startWidth: sidebar.getBoundingClientRect().width };
    handle.setPointerCapture?.(event.pointerId);
    event.preventDefault();
  });

  document.addEventListener("pointermove", (event) => {
    if (!resizeState) return;
    setSidebarWidth(resizeState.startWidth + event.clientX - resizeState.startX);
  });

  document.addEventListener("pointerup", () => { resizeState = null; });
  document.addEventListener("pointercancel", () => { resizeState = null; });
})();
