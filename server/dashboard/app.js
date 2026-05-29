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
    return `
      <article class="card">
        <div>
          <h2>${agent.name}</h2>
          <p>Last seen: ${lastSeen}</p>
        </div>
        <span class="status ${status}">${status}</span>
      </article>
    `;
  }).join("");
}

document.querySelector("#refresh").addEventListener("click", loadAgents);
loadAgents();
setInterval(loadAgents, 15000);
