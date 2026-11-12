// 首帧前应用主题，避免闪烁（与 src/lib/theme.ts 的存储键一致）。
// 独立成文件而不是内联脚本，这样 CSP 可以只放行同源脚本。
(function () {
  var t = "system";
  try { t = localStorage.getItem("relay-theme") || "system"; } catch (e) {}
  var dark = t === "dark" || (t === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  if (dark) document.documentElement.classList.add("dark");
})();
