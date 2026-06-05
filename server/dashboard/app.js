async function loadAgents() {
  const container = document.querySelector("#agents");
  container.innerHTML = "<div class='empty'>Loading agents...</div>";

  const response = await fetch("/api/agents");
  const agents = await response.json();

  if (!agents.length) {
    container.innerHTML = "<div class='empty'>No agents connected yet.</div>";
    return;
  }

  container.innerHTML = agents.map((agent) => {
    const status = agent.online ? "online" : "offline";
    const lastSeen = new Date(agent.lastSeen).toLocaleString();
    const ssh = agent.ssh
      ? `<p class="command">ssh -p ${agent.ssh.port} user@YOUR_VPS_IP</p>`
      : "<p>No SSH route configured</p>";
    return `
      <article class="card">
        <div>
          <h2>${agent.name}</h2>
          <p>Last seen: ${lastSeen}</p>
          ${ssh}
        </div>
        <span class="status ${status}">${status}</span>
      </article>
    `;
  }).join("");
}

async function clearOfflineAgents() {
  const notice = document.querySelector("#notice");
  const button = document.querySelector("#clear-offline");
  button.disabled = true;
  notice.textContent = "Clearing offline agents...";

  try {
    const response = await fetch("/api/agents/clear-offline", { method: "POST" });
    if (!response.ok) {
      throw new Error(`Request failed with ${response.status}`);
    }
    const result = await response.json();
    notice.textContent = `Cleared ${result.cleared} offline agent${result.cleared === 1 ? "" : "s"}.`;
    await loadAgents();
  } catch (error) {
    notice.textContent = "Could not clear offline agents.";
    console.error(error);
  } finally {
    button.disabled = false;
  }
}

document.querySelector("#refresh").addEventListener("click", loadAgents);
document.querySelector("#clear-offline").addEventListener("click", clearOfflineAgents);
loadAgents();
setInterval(loadAgents, 15000);
