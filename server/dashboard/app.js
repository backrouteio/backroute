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

document.querySelector("#refresh").addEventListener("click", loadAgents);
loadAgents();
setInterval(loadAgents, 15000);
