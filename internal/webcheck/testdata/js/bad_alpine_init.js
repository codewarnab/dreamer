// This file uses the broken alpine:init pattern.
document.addEventListener("alpine:init", function () {
  Alpine.store("sse", {
    connected: false,
  });
});
