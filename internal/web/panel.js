"use strict";

for (const button of document.querySelectorAll("button[data-argv]")) {
  button.addEventListener("click", async () => {
    const result = button.parentElement.querySelector(".copy-result");
    try {
      await navigator.clipboard.writeText(button.dataset.argv);
      result.textContent = "Comando copiado. Nada foi executado.";
      window.setTimeout(() => {
        result.textContent = "";
      }, 2000);
    } catch (error) {
      result.textContent = "Não foi possível copiar. Selecione o comando.";
    }
  });
}

for (const element of document.querySelectorAll("time[datetime]")) {
  const date = new Date(element.dateTime);
  if (!Number.isNaN(date.getTime())) {
    element.textContent = date.toLocaleString();
  }
}
