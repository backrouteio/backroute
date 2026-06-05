function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

async function loadAgents() {
  const container = document.querySelector("#agents");
  container.innerHTML = "<div class='empty'>Loading agents...</div>";

  const response = await fetch("/api/agents");
  const agents = await response.json();

  if (!agents.length) {
    container.innerHTML = "<div class='empty'>No nodes yet. Create a node to assign the first SSH port.</div>";
    return;
  }

  container.innerHTML = agents.map((agent) => {
    const status = agent.online ? "online" : "offline";
    const lastSeen = agent.lastSeen && !agent.lastSeen.startsWith("0001-")
      ? new Date(agent.lastSeen).toLocaleString()
      : "-";
    const connectedAt = agent.connectedAt && !agent.connectedAt.startsWith("0001-")
      ? new Date(agent.connectedAt).toLocaleString()
      : "-";
    const sourceIp = escapeHtml(agent.sourceIp || "-");
    const location = escapeHtml(agent.location || "Unknown");
    const activeFor = escapeHtml(agent.activeFor || "-");
    const host = window.location.hostname || "YOUR_VPS_IP";
    const agentServer = `${window.location.protocol === "https:" ? "wss" : "ws"}://${host}:8080/agent`;
    const ssh = agent.ssh
      ? `
          <div class="connect">
            <h3>Connect this node agent</h3>
            <p class="command">go run . -server ${escapeHtml(agentServer)} -token &lt;BACKROUTE_TOKEN&gt; -name ${escapeHtml(agent.name)} -ssh-target ${escapeHtml(agent.ssh.target)}</p>
            <h3>SSH through BackRoute</h3>
            <p class="command">ssh -p ${agent.ssh.port} user@${escapeHtml(host)}</p>
          </div>
        `
      : "<p>No SSH route configured</p>";
    const deleteButton = agent.ssh
      ? `<button class="danger" type="button" data-route-delete="${escapeHtml(agent.name)}">Delete node</button>`
      : "";
    return `
      <article class="card">
        <div>
          <h2>${escapeHtml(agent.name)}</h2>
          <dl class="meta">
            <div><dt>Source IP</dt><dd>${sourceIp}</dd></div>
            <div><dt>Location</dt><dd>${location}</dd></div>
            <div><dt>Connected</dt><dd>${connectedAt}</dd></div>
            <div><dt>Last seen</dt><dd>${lastSeen}</dd></div>
            <div><dt>Active for</dt><dd>${activeFor}</dd></div>
          </dl>
          ${ssh}
        </div>
        <div class="card-actions">
          <span class="status ${status}">${status}</span>
          ${deleteButton}
        </div>
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

async function createNode(event) {
  event.preventDefault();

  const notice = document.querySelector("#notice");
  const form = event.currentTarget;
  const submit = form.querySelector("button[type='submit']");
  const name = document.querySelector("#node-name").value.trim();
  const target = document.querySelector("#node-target").value.trim() || "127.0.0.1:22";

  submit.disabled = true;
  notice.textContent = `Creating ${name}...`;

  try {
    const response = await fetch("/api/routes", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, target }),
    });
    if (!response.ok) {
      throw new Error(await response.text());
    }
    const result = await response.json();
    notice.textContent = `${result.route.agentName} created on SSH port ${result.route.port}.`;
    document.querySelector("#node-name").value = "";
    await loadAgents();
  } catch (error) {
    notice.textContent = error.message || "Could not create node.";
    console.error(error);
  } finally {
    submit.disabled = false;
  }
}

async function deleteNode(name) {
  const notice = document.querySelector("#notice");
  if (!window.confirm(`Delete ${name}? The SSH port will be released.`)) {
    return;
  }

  notice.textContent = `Deleting ${name}...`;

  try {
    const response = await fetch(`/api/routes/${encodeURIComponent(name)}`, { method: "DELETE" });
    if (!response.ok) {
      throw new Error(await response.text());
    }
    notice.textContent = `${name} deleted.`;
    await loadAgents();
  } catch (error) {
    notice.textContent = error.message || "Could not delete node.";
    console.error(error);
  }
}

document.querySelector("#refresh").addEventListener("click", loadAgents);
document.querySelector("#clear-offline").addEventListener("click", clearOfflineAgents);
document.querySelector("#create-node").addEventListener("submit", createNode);
document.querySelector("#agents").addEventListener("click", (event) => {
  const button = event.target.closest("[data-route-delete]");
  if (!button) {
    return;
  }
  deleteNode(button.dataset.routeDelete);
});
loadAgents();
setInterval(loadAgents, 15000);
