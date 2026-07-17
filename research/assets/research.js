const root = document.documentElement;
const toggle = document.querySelector("#theme-toggle");
const savedTheme = localStorage.getItem("amux-research-theme");
if (savedTheme) root.dataset.theme = savedTheme;

toggle?.addEventListener("click", () => {
  const systemDark = matchMedia("(prefers-color-scheme: dark)").matches;
  const currentDark = root.dataset.theme ? root.dataset.theme === "dark" : systemDark;
  const next = currentDark ? "light" : "dark";
  root.dataset.theme = next;
  localStorage.setItem("amux-research-theme", next);
});

const progress = document.querySelector(".reading-progress span");
function updateProgress() {
  if (!progress) return;
  const available = document.documentElement.scrollHeight - innerHeight;
  const amount = available > 0 ? Math.min(1, scrollY / available) : 0;
  progress.style.width = `${amount * 100}%`;
}
addEventListener("scroll", updateProgress, { passive: true });
updateProgress();

const tocLinks = [...document.querySelectorAll(".toc-link")];
const headings = tocLinks
  .map((link) => document.getElementById(link.hash.slice(1)))
  .filter(Boolean);
if (headings.length) {
  const observer = new IntersectionObserver((entries) => {
    const visible = entries.filter((entry) => entry.isIntersecting).at(-1);
    if (!visible) return;
    tocLinks.forEach((link) => link.classList.toggle("active", link.hash === `#${visible.target.id}`));
  }, { rootMargin: "-15% 0px -72%" });
  headings.forEach((heading) => observer.observe(heading));
}

document.querySelectorAll(".research-article a").forEach((link) => {
  if (link.origin !== location.origin && !link.href.startsWith("file:")) {
    link.target = "_blank";
    link.rel = "noreferrer";
  }
});

const search = document.querySelector("#page-search");
search?.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || !search.value.trim()) return;
  event.preventDefault();
  window.find(search.value.trim(), false, event.shiftKey, true, false, true, false);
});
addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k" && search) {
    event.preventDefault();
    search.focus();
  }
});

const mobileButton = document.querySelector(".mobile-toc-button");
const mobileToc = document.querySelector("#mobile-toc");
mobileButton?.addEventListener("click", () => {
  const open = mobileButton.getAttribute("aria-expanded") === "true";
  mobileButton.setAttribute("aria-expanded", String(!open));
  mobileToc.hidden = open;
});
mobileToc?.addEventListener("click", (event) => {
  if (!event.target.closest("a")) return;
  mobileButton.setAttribute("aria-expanded", "false");
  mobileToc.hidden = true;
});
