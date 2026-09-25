document.addEventListener("submit", function (event) {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || form.dataset.fabricAction !== "back") {
    return;
  }
  event.preventDefault();
  window.history.back();
});
