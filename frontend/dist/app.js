const state = { proposals: [], selectedId: null, lastPlan: null, lastPlanOptions: null, lastExecution: null, aiProfiles: [], aiSuggestions: {}, executionInProgress: false };
const preferencesKey = "myfilesorter.preferences.v1";

const $ = (selector) => document.querySelector(selector);
const api = () => window.go?.main?.App || browserPreviewAPI;

const previewProposal = {
  id: "browser-preview",
  sourceRoot: "/Vorschau/Unsortiert",
  groupPath: "/Vorschau/Unsortiert/Der Name des Windes",
  status: "review_required",
  confidence: 0.91,
  warnings: [],
  metadata: {
    title: "Der Name des Windes",
    author: "Patrick Rothfuss",
    series: "Die Königsmörder-Chronik",
    seriesSequence: "1",
    editionInfo: "Ungekürzt",
    narrator: "Stefan Kaminski",
    language: "de",
    asin: "B004V0Q9ZA",
    isbn: "",
    evidence: {
      title: { value: "Der Name des Windes", source: "album_tag", confidence: 0.94 },
      author: { value: "Patrick Rothfuss", source: "album_artist_tag", confidence: 0.96 },
      series: { value: "Die Königsmörder-Chronik", source: "series_tag", confidence: 0.88 },
      seriesSequence: { value: "1", source: "series_part_tag", confidence: 0.91 },
      editionInfo: { value: "Ungekürzt", source: "folder", confidence: 0.75 },
      narrator: { value: "Stefan Kaminski", source: "narrator_tag", confidence: 0.9 },
      language: { value: "de", source: "language_tag", confidence: 0.95 },
      asin: { value: "B004V0Q9ZA", source: "asin_tag", confidence: 0.99 },
    },
  },
  files: [
    { path: "/Vorschau/Unsortiert/name_des_windes_01.m4b", name: "name_des_windes_01.m4b", extension: ".m4b", size: 734003200 },
    { path: "/Vorschau/Unsortiert/name_des_windes_02.m4b", name: "name_des_windes_02.m4b", extension: ".m4b", size: 681574400 },
  ],
  companions: [
    { path: "/Vorschau/Unsortiert/Der Name des Windes/Der Name des Windes.epub", name: "Der Name des Windes.epub", extension: ".epub", size: 3145728, kind: "ebook" },
    { path: "/Vorschau/Unsortiert/Der Name des Windes/folder.jpg", name: "folder.jpg", extension: ".jpg", size: 245760, kind: "discard" },
  ],
};

const previewCandidates = {
  audible: [
    {
      id: "audible:B004V0Q9ZA", provider: "audible", title: "Der Name des Windes", author: "Patrick Rothfuss",
      series: "Die Königsmörder-Chronik", seriesSequence: "1", narrator: "Stefan Kaminski", language: "german",
      asin: "B004V0Q9ZA", coverUrl: "", durationMinutes: 1690, publishedYear: "2011", confidence: 0.97,
    },
    {
      id: "audible:B00PREVIEW2", provider: "audible", title: "The Name of the Wind", author: "Patrick Rothfuss",
      series: "The Kingkiller Chronicle", seriesSequence: "1", narrator: "Nick Podehl", language: "english",
      asin: "B00PREVIEW2", coverUrl: "", durationMinutes: 1725, confidence: 0.76,
    },
  ],
  google_books: [
    {
      id: "google_books:preview", provider: "google_books", title: "Der Name des Windes", author: "Patrick Rothfuss",
      isbn: "9783608938159", language: "de", publisher: "Klett-Cotta", publishedYear: "2008", confidence: 0.89,
    },
  ],
};

const previewAIProfiles = [{ id: "preview-ollama", name: "Lokales Ollama", provider: "ollama", baseUrl: "http://127.0.0.1:11434", model: "qwen3:8b", hasApiKey: false, isDefault: true }];

const browserPreviewAPI = {
  async SelectDirectory(title) {
    return title.includes("Ziel") ? "/Vorschau/Audiobookshelf" : "/Vorschau/Unsortiert";
  },
  async Scan(source) {
    const proposal = structuredClone(previewProposal);
    proposal.sourceRoot = source;
    return {
      source,
      proposals: [proposal],
      summary: { books: 1, files: 2, ebooks: 1, sidecars: 1, bytes: 1415577600, metadataAvailable: true },
      globalNotes: ["Browser-Vorschau mit Beispieldaten – es werden keine lokalen Dateien gelesen."],
    };
  },
  async UpdateProposal(id, metadata) {
    const proposal = state.proposals.find((item) => item.id === id);
    return { ...proposal, metadata, status: "review_required" };
  },
  async SetProposalStatus(id, status) {
    const proposal = state.proposals.find((item) => item.id === id);
    return { ...proposal, status };
  },
  async SearchOnline(id, provider) {
    return structuredClone(previewCandidates[provider] || []);
  },
  async ApplyOnlineCandidate(proposalId, candidateId) {
    const proposal = state.proposals.find((item) => item.id === proposalId);
    const candidate = Object.values(previewCandidates).flat().find((item) => item.id === candidateId);
    const metadata = { ...proposal.metadata, evidence: { ...proposal.metadata.evidence } };
    const mapping = { title: "title", author: "author", series: "series", seriesSequence: "seriesSequence", narrator: "narrator", language: "language", asin: "asin", isbn: "isbn" };
    for (const [candidateKey, metadataKey] of Object.entries(mapping)) {
      if (!candidate?.[candidateKey]) continue;
      metadata[metadataKey] = candidate[candidateKey];
      metadata.evidence[metadataKey] = { value: candidate[candidateKey], source: `online:${candidate.provider}`, confidence: candidate.confidence };
    }
    return { ...proposal, metadata, confidence: candidate.confidence, status: "review_required" };
  },
  async BuildPlan(target, options = {}) {
    const confirmed = state.proposals.filter((item) => item.status === "confirmed");
    const audio = confirmed.flatMap((proposal) => proposal.files.map((file, index) => ({
      proposalId: proposal.id,
      action: "move", category: "audio",
      source: file.path,
      target: `${target}/${proposal.metadata.author}/${proposal.metadata.series}/${formattedSequence(proposal.metadata.seriesSequence, proposal, options.bookNumberWidth)} - ${proposal.metadata.title}/${targetAudioName(file, index, proposal, options)}`,
      size: file.size,
    })));
    const ebooks = options.moveEbooks ? confirmed.flatMap((proposal) => (proposal.companions || []).filter((file) => file.kind === "ebook").map((file) => ({
      proposalId: proposal.id, action: "move", category: "ebook", source: file.path,
      target: `${target}/# Ebooks/${proposal.metadata.author}/${proposal.metadata.series}/${formattedSequence(proposal.metadata.seriesSequence, proposal, options.bookNumberWidth)} - ${proposal.metadata.title}/${proposal.metadata.title}${file.extension}`,
      size: file.size,
    }))) : [];
    const cleanup = options.cleanupSidecars ? confirmed.flatMap((proposal) => (proposal.companions || []).filter((file) => file.kind === "discard").map((file) => ({
      proposalId: proposal.id, action: "remove", category: "sidecar", source: file.path, target: "", size: file.size,
    }))) : [];
    const operations = [...audio, ...ebooks, ...cleanup];
    return {
      targetRoot: target,
      operations,
      totalBytes: operations.reduce((total, item) => total + item.size, 0),
      warnings: operations.length ? [] : ["Bitte zuerst den Beispielvorschlag bestätigen."],
      executable: operations.length > 0,
    };
  },
  async ExecutePlan() {
    const operations = state.lastPlan?.operations || [];
    let completedBytes = 0;
    handleExecutionProgress({ status: "checking", completed: 0, total: operations.length, completedBytes: 0, totalBytes: state.lastPlan?.totalBytes || 0 });
    for (let index = 0; index < operations.length; index += 1) {
      await new Promise((resolve) => setTimeout(resolve, 180));
      completedBytes += operations[index].size || 0;
      handleExecutionProgress({
        status: "moving", completed: index + 1, total: operations.length,
        completedBytes, totalBytes: state.lastPlan?.totalBytes || 0,
        currentSource: operations[index].source, currentTarget: operations[index].target,
      });
    }
    return {
      journalId: "browser-preview-journal", status: "completed",
      completed: state.lastPlan?.operations.length || 0,
      total: state.lastPlan?.operations.length || 0,
      totalBytes: state.lastPlan?.totalBytes || 0,
      warnings: ["Browser-Vorschau: Es wurden keine Dateien verändert."],
    };
  },
  async UndoExecution(journalId) {
    return {
      journalId, status: "undone", completed: 0,
      total: state.lastPlan?.operations.length || 0, totalBytes: 0,
      warnings: ["Browser-Vorschau: Es wurden keine Dateien verändert."],
    };
  },
  async GetSessionLog() {
    return {
      sessionId: "browser-preview",
      filePath: "/Vorschau/MyFileSorter/logs/session-browser-preview.jsonl",
      entries: [
        { timestamp: new Date().toISOString(), level: "info", component: "app", message: "Browser-Vorschau gestartet", details: {} },
        { timestamp: new Date().toISOString(), level: "info", component: "scan", message: "Lokaler Scan abgeschlossen", details: { books: "1", audioFiles: "2" } },
      ],
    };
  },
  async GetAIProfiles() { return structuredClone(previewAIProfiles); },
  async SaveAIProfile(input) {
    const profile = { ...input, id: input.id || `preview-${Date.now()}`, hasApiKey: Boolean(input.apiKey) };
    delete profile.apiKey;
    const index = previewAIProfiles.findIndex((item) => item.id === profile.id);
    if (profile.isDefault) previewAIProfiles.forEach((item) => { item.isDefault = false; });
    if (index >= 0) previewAIProfiles[index] = profile; else previewAIProfiles.push(profile);
    return structuredClone(profile);
  },
  async DeleteAIProfile(id) {
    const index = previewAIProfiles.findIndex((item) => item.id === id);
    if (index >= 0) previewAIProfiles.splice(index, 1);
  },
  async TestAIProfile() {},
  async AnalyzeWithAI(proposalId, profileId) {
    const suggestion = {
      proposalId, profileId, title: "Der Name des Windes", author: "Patrick Rothfuss",
      series: "Die Königsmörder-Chronik", seriesSequence: "1", editionInfo: "Ungekürzt",
      narrator: "Stefan Kaminski", language: "de", suggestedSearchTitle: "Der Name des Windes",
      suggestedSearchAuthor: "Patrick Rothfuss", confidence: 0.92,
      reasoning: "Ordnername, Dateinamen und vorhandene Album-Metadaten stimmen überein.",
    };
    state.aiSuggestions[proposalId] = suggestion;
    return structuredClone(suggestion);
  },
  async ApplyAISuggestion(proposalId) {
    const proposal = state.proposals.find((item) => item.id === proposalId);
    const suggestion = state.aiSuggestions[proposalId];
    const metadata = { ...proposal.metadata, evidence: { ...proposal.metadata.evidence } };
    for (const key of ["title", "author", "series", "seriesSequence", "editionInfo", "narrator", "language"]) {
      if (!suggestion?.[key]) continue;
      metadata[key] = suggestion[key];
      metadata.evidence[key] = { value: suggestion[key], source: `ai:${suggestion.profileId}`, confidence: suggestion.confidence };
    }
    return { ...proposal, metadata, confidence: suggestion.confidence, status: "review_required" };
  },
};

function escapeHTML(value = "") {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function formatBytes(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** index).toFixed(index > 1 ? 1 : 0)} ${units[index]}`;
}

function statusLabel(status) {
  return {
    review_required: "Prüfen",
    confirmed: "Bestätigt",
    excluded: "Ausgeschlossen",
    imported: "Importiert",
    conflict: "Konflikt",
    error: "Fehler",
  }[status] || status;
}

function selectedNumberWidth(id, fallback = 2) {
  const value = Number.parseInt($(id)?.value ?? fallback, 10);
  return Number.isFinite(value) ? value : fallback;
}

function savePreferences() {
  try {
    localStorage.setItem(preferencesKey, JSON.stringify({
      source: $("#source")?.value || "",
      target: $("#target")?.value || "",
      ...planOptions(),
    }));
  } catch (_error) {
    // Die App bleibt auch ohne verfügbaren Webview-Speicher vollständig nutzbar.
  }
}

function restorePreferences() {
  try {
    const preferences = JSON.parse(localStorage.getItem(preferencesKey) || "null");
    if (!preferences) return;
    $("#source").value = preferences.source || "";
    $("#target").value = preferences.target || "";
    if (Number.isFinite(preferences.bookNumberWidth)) $("#book-number-width").value = String(preferences.bookNumberWidth);
    if (Number.isFinite(preferences.trackNumberWidth)) $("#track-number-width").value = String(preferences.trackNumberWidth);
    if (["source_title", "book_title"].includes(preferences.audioFileNaming)) $("#audio-file-naming").value = preferences.audioFileNaming;
    if (typeof preferences.moveEbooks === "boolean") $("#move-ebooks").checked = preferences.moveEbooks;
    if (typeof preferences.cleanupSidecars === "boolean") $("#cleanup-sidecars").checked = preferences.cleanupSidecars;
  } catch (_error) {
    // Beschädigte oder ältere Einstellungen werden ignoriert.
  }
}

function automaticBookWidth(proposal) {
  const values = state.proposals
    .filter((item) => item.metadata.author.trim().toLocaleLowerCase() === proposal.metadata.author.trim().toLocaleLowerCase()
      && item.metadata.series.trim().toLocaleLowerCase() === proposal.metadata.series.trim().toLocaleLowerCase())
    .map((item) => Number.parseInt(String(item.metadata.seriesSequence).replace(",", ".").split(".")[0], 10))
    .filter(Number.isFinite);
  return String(Math.max(1, ...values)).length;
}

function automaticTrackWidth(files) {
  const maximum = Math.max(files.length, ...(files.map((file) => Number(file.track) || 0)), 1);
  return String(maximum).length;
}

function formattedSequence(value, proposal, requestedWidth = selectedNumberWidth("#book-number-width")) {
  const text = String(value || "").trim().replace(",", ".");
  if (!text) return "";
  const [whole, fraction] = text.split(".", 2);
  const numeric = Number.parseInt(whole, 10);
  if (!Number.isFinite(numeric)) return text;
  const cleanedFraction = fraction?.replace(/0+$/, "") || "";
  const width = requestedWidth < 0 && proposal ? automaticBookWidth(proposal) : Math.max(1, requestedWidth || 2);
  return `${String(numeric).padStart(width, "0")}${cleanedFraction ? `.${cleanedFraction}` : ""}`;
}

function displayBookTitle(proposal) {
  const sequence = formattedSequence(proposal.metadata.seriesSequence, proposal);
  return sequence ? `${sequence} - ${proposal.metadata.title}` : proposal.metadata.title;
}

function targetAudioName(file, index, proposal, options = planOptions()) {
  const requested = Number(options.trackNumberWidth ?? 2);
  const width = requested < 0 ? automaticTrackWidth(proposal.files) : Math.max(1, requested || 2);
  let number = Number(file.track) || index + 1;
  let title = proposal.metadata.title;
  if (options.audioFileNaming === "source_title") {
    const extensionPattern = new RegExp(`${String(file.extension || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`, "i");
    let sourceTitle = String(file.name || "").replace(extensionPattern, "");
    const match = sourceTitle.match(/^\s*(\d+)\s*(?:[-._]+\s*)?(.*)$/);
    if (match) {
      number = Number(file.track) || Number.parseInt(match[1], 10) || index + 1;
      sourceTitle = match[2];
    }
    title = sourceTitle.replaceAll("_", " ").replaceAll(".", " ").replace(/\s+/g, " ").replace(/^[- ]+|[- ]+$/g, "") || "Track";
  }
  return `${String(number).padStart(Math.max(width, String(number).length), "0")} - ${title}${String(file.extension || "").toLocaleLowerCase()}`;
}

function displaySourcePath(proposal) {
  const root = String(proposal.sourceRoot || "").replaceAll("\\", "/").replace(/\/+$/, "");
  const group = String(proposal.groupPath || "").replaceAll("\\", "/");
  if (root && group.startsWith(`${root}/`)) return group.slice(root.length + 1);
  return group || root || "Unbekannter Quellordner";
}

function currentSourceFolder(proposal) {
  const group = String(proposal.groupPath || "");
  if ((proposal.files || []).length === 1 && group === proposal.files[0].path) return proposal.sourceRoot || group;
  return group || proposal.sourceRoot || "Unbekannter Quellordner";
}

function toast(message, isError = false) {
  const element = $("#toast");
  element.textContent = message;
  element.classList.toggle("error", isError);
  element.classList.remove("hidden");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => element.classList.add("hidden"), 3800);
}

async function showSessionLog() {
  const overlay = $("#log-overlay");
  const content = $("#log-content");
  overlay.classList.remove("hidden");
  content.innerHTML = `<div class="online-loading"><span></span>Log wird geladen …</div>`;
  try {
    const snapshot = await api().GetSessionLog();
    const entries = [...(snapshot.entries || [])].reverse();
    content.innerHTML = `
      <div class="log-meta"><span>Sitzung <strong>${escapeHTML(snapshot.sessionId)}</strong></span><span title="${escapeHTML(snapshot.filePath)}">${escapeHTML(snapshot.filePath || "Nur im Arbeitsspeicher")}</span></div>
      ${snapshot.warning ? `<div class="warning-list">${escapeHTML(snapshot.warning)}</div>` : ""}
      <div class="log-entries">
        ${entries.length ? entries.map(logEntry).join("") : `<div class="no-candidates">Noch keine Logeinträge in dieser Sitzung.</div>`}
      </div>
    `;
  } catch (error) {
    content.innerHTML = `<div class="online-error"><strong>Log konnte nicht geladen werden</strong><p>${escapeHTML(String(error))}</p></div>`;
  }
}

function logEntry(entry) {
  const time = new Date(entry.timestamp).toLocaleTimeString("de-DE", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  const details = Object.entries(entry.details || {});
  return `
    <article class="log-entry ${escapeHTML(entry.level)}">
      <div class="log-entry-head"><time>${escapeHTML(time)}</time><b>${escapeHTML(entry.level)}</b><span>${escapeHTML(entry.component)}</span></div>
      <strong>${escapeHTML(entry.message)}</strong>
      ${details.length ? `<dl>${details.map(([key, value]) => `<div><dt>${escapeHTML(key)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl>` : ""}
    </article>
  `;
}

async function chooseDirectory(input, title) {
  try {
    const path = await api().SelectDirectory(title);
    if (path) {
      input.value = path;
      savePreferences();
    }
  } catch (error) {
    toast(String(error), true);
  }
}

async function scan() {
  const source = $("#source").value.trim();
  if (!source) return toast("Bitte zuerst einen Quellordner auswählen.", true);
  const button = $("#scan");
  savePreferences();
  button.disabled = true;
  button.textContent = "Scan läuft …";
  try {
    const result = await api().Scan(source);
    state.proposals = result.proposals || [];
    state.selectedId = state.proposals[0]?.id || null;
    $("#workspace").classList.remove("hidden");
    $("#plan-section").classList.remove("hidden");
    const extras = [
      result.summary.ebooks ? `${result.summary.ebooks} E-Book${result.summary.ebooks === 1 ? "" : "s"}` : "",
      result.summary.sidecars ? `${result.summary.sidecars} Begleitdatei${result.summary.sidecars === 1 ? "" : "en"}` : "",
    ].filter(Boolean).join(" · ");
    $("#summary").innerHTML = `<strong>${result.summary.books}</strong> Bücher · <strong>${result.summary.files}</strong> Audiodateien · ${formatBytes(result.summary.bytes)}${extras ? ` · ${extras}` : ""}`;
    const notice = $("#notice");
    if (result.globalNotes?.length) {
      notice.textContent = result.globalNotes.join(" ");
      notice.classList.remove("hidden");
    } else {
      notice.classList.add("hidden");
    }
    render();
    $("#workspace").scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    toast(String(error), true);
  } finally {
    button.disabled = false;
    button.innerHTML = "Lokal scannen <span>→</span>";
  }
}

function render() {
  renderList();
  renderDetail();
}

function renderList() {
  const list = $("#book-list");
  if (!state.proposals.length) {
    list.innerHTML = `<div class="empty-list">Keine unterstützten Audiodateien gefunden.</div>`;
    return;
  }
  list.innerHTML = state.proposals.map((proposal) => `
    <div class="book-item ${proposal.id === state.selectedId ? "active" : ""}" data-id="${proposal.id}" role="button" tabindex="0">
      <input class="book-ready" type="checkbox" aria-label="Für Import auswählen" data-ready-id="${proposal.id}"
        ${proposal.status === "confirmed" || proposal.status === "imported" ? "checked" : ""}
        ${proposal.status === "imported" ? "disabled" : ""} />
      <span class="book-copy">
        <strong>${escapeHTML(displayBookTitle(proposal))}</strong>
        <small>${escapeHTML(proposal.metadata.author)} · ${proposal.files.length} Datei${proposal.files.length === 1 ? "" : "en"}</small>
        <small class="source-path" title="${escapeHTML(proposal.groupPath)}">${escapeHTML(displaySourcePath(proposal))}</small>
      </span>
      <span class="status-pill ${proposal.status}">${statusLabel(proposal.status)}</span>
    </div>
  `).join("");
  list.querySelectorAll(".book-item").forEach((item) => {
    item.addEventListener("click", () => {
      state.selectedId = item.dataset.id;
      render();
    });
    item.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        state.selectedId = item.dataset.id;
        render();
      }
    });
  });
  list.querySelectorAll(".book-ready").forEach((checkbox) => {
    checkbox.addEventListener("click", (event) => event.stopPropagation());
    checkbox.addEventListener("change", () => toggleReady(checkbox.dataset.readyId, checkbox.checked));
  });
}

function evidence(meta, field) {
  const item = meta.evidence?.[field];
  if (!item?.source) return "Keine lokale Quelle";
  return `${item.source.replaceAll("_", " ")} · ${Math.round(item.confidence * 100)} %`;
}

function renderDetail() {
  const detail = $("#detail");
  const proposal = state.proposals.find((item) => item.id === state.selectedId);
  if (!proposal) {
    detail.className = "detail empty";
    detail.innerHTML = "<p>Wähle links ein Hörbuch aus.</p>";
    return;
  }
  const m = proposal.metadata;
  detail.className = "detail";
  detail.innerHTML = `
    <div class="detail-header">
      <div>
        <p class="detail-kicker">LOKALER VORSCHLAG · ${Math.round(proposal.confidence * 100)} %</p>
        <h3>${escapeHTML(displayBookTitle(proposal))}</h3>
        <p>${escapeHTML(m.author)}</p>
      </div>
      <span class="status-pill ${proposal.status}">${statusLabel(proposal.status)}</span>
    </div>
    <div class="source-location">
      <span>AKTUELLER ORDNER</span>
      <strong title="${escapeHTML(currentSourceFolder(proposal))}">${escapeHTML(currentSourceFolder(proposal))}</strong>
    </div>
    ${proposal.warnings?.length ? `<div class="warning-list">${proposal.warnings.map(escapeHTML).join("<br>")}</div>` : ""}
    <form id="metadata-form" class="metadata-form">
      ${field("Titel", "title", m.title, evidence(m, "title"), true)}
      ${field("Autor", "author", m.author, evidence(m, "author"), true)}
      ${field("Serie", "series", m.series, evidence(m, "series"))}
      ${field("Band", "seriesSequence", m.seriesSequence, evidence(m, "seriesSequence"))}
      ${field("Info", "editionInfo", m.editionInfo, evidence(m, "editionInfo"))}
      ${field("Sprecher", "narrator", m.narrator, evidence(m, "narrator"))}
      ${field("Sprache", "language", m.language, evidence(m, "language"))}
      ${field("ASIN", "asin", m.asin, evidence(m, "asin"))}
      ${field("ISBN", "isbn", m.isbn, evidence(m, "isbn"))}
    </form>
    <div class="file-box">
      <p>QUELLDATEIEN</p>
      ${proposal.files.map((file, index) => `<div class="file-preview"><span>${String(index + 1).padStart(2, "0")}</span><span class="file-name-pair"><strong title="${escapeHTML(file.path)}">${escapeHTML(file.name)}</strong><em>→ ${escapeHTML(targetAudioName(file, index, proposal))}</em></span><small>${formatBytes(file.size)}</small></div>`).join("")}
    </div>
    ${renderCompanions(proposal.companions || [])}
    <div class="online-panel hidden" id="online-panel"></div>
    <div class="actions">
      <button class="ghost" id="online">Online suchen</button>
      <button class="ghost" id="ai">Mit AI analysieren</button>
      <label class="ready-toggle"><input type="checkbox" id="detail-ready" ${proposal.status === "confirmed" || proposal.status === "imported" ? "checked" : ""} ${proposal.status === "imported" ? "disabled" : ""} /> Für Import auswählen</label>
      <span class="action-spacer"></span>
      <button class="ghost danger" id="exclude">Überspringen</button>
      <button class="secondary" id="save">Änderungen speichern</button>
      <button class="primary compact" id="confirm">Fertig & auswählen</button>
    </div>
  `;
  $("#metadata-form").addEventListener("submit", (event) => event.preventDefault());
  $("#save").addEventListener("click", () => saveProposal(proposal, false));
  $("#confirm").addEventListener("click", () => saveProposal(proposal, true));
  $("#detail-ready").addEventListener("change", (event) => {
    if (event.target.checked) saveProposal(proposal, true);
    else setStatus(proposal.id, "review_required");
  });
  $("#exclude").addEventListener("click", () => setStatus(proposal.id, "excluded"));
  $("#online").addEventListener("click", () => showProviderChoice(proposal));
  $("#ai").addEventListener("click", () => showAIChoice(proposal));
}

async function toggleReady(id, checked) {
  try {
    replaceProposal(await api().SetProposalStatus(id, checked ? "confirmed" : "review_required"));
    toast(checked ? "Hörbuch für den Import ausgewählt." : "Hörbuch bleibt im Quellordner.");
  } catch (error) {
    toast(String(error), true);
    render();
  }
}

function renderCompanions(companions) {
  if (!companions.length) return "";
  return `
    <div class="companion-box">
      <p>WEITERE DATEIEN</p>
      ${companions.map((file) => `<div><span class="companion-kind ${file.kind}">${file.kind === "ebook" ? "E-Book" : "Begleitdatei"}</span><strong>${escapeHTML(file.name)}</strong><small>${formatBytes(file.size)}</small></div>`).join("")}
    </div>
  `;
}

function showProviderChoice(proposal) {
  const panel = $("#online-panel");
  panel.classList.remove("hidden");
  panel.innerHTML = `
    <div class="online-heading">
      <div><p class="detail-kicker">BEWUSSTE ONLINE-ANFRAGE</p><strong>Metadatenanbieter wählen</strong></div>
      <button class="panel-close" aria-label="Schließen">×</button>
    </div>
    <p class="online-copy">Erst mit deiner Auswahl wird eine Suchanfrage für „${escapeHTML(proposal.metadata.title)}“ gesendet.</p>
    <div class="provider-actions">
      <button class="provider-button audible-provider"><b>A</b><span><strong>Audible Deutschland</strong><small>Hörbuchausgabe, Sprecher, Laufzeit und Serie</small></span></button>
      <button class="provider-button google-provider"><b>G</b><span><strong>Google Books</strong><small>Buchtitel, Autor, ISBN und Verlag</small></span></button>
    </div>
  `;
  panel.querySelector(".panel-close").addEventListener("click", () => panel.classList.add("hidden"));
  panel.querySelector(".audible-provider").addEventListener("click", () => searchOnline(proposal, "audible"));
  panel.querySelector(".google-provider").addEventListener("click", () => searchOnline(proposal, "google_books"));
  panel.scrollIntoView({ behavior: "smooth", block: "nearest" });
}

async function showAIChoice(proposal) {
  const panel = $("#online-panel");
  panel.classList.remove("hidden");
  panel.innerHTML = `<div class="online-loading"><span></span>AI-Profile werden geladen …</div>`;
  try {
    state.aiProfiles = await api().GetAIProfiles();
    if (!state.aiProfiles.length) {
      panel.innerHTML = `
        <div class="online-heading"><div><p class="detail-kicker">BEWUSSTE AI-ANALYSE</p><strong>Noch kein AI-Profil</strong></div><button class="panel-close" aria-label="Schließen">×</button></div>
        <p class="online-copy">Lege zuerst ein lokales Ollama-/LM-Studio-Profil oder eine OpenAI-kompatible Verbindung an.</p>
        <button class="secondary open-profile-settings">AI-Profile verwalten</button>`;
      panel.querySelector(".panel-close").addEventListener("click", () => panel.classList.add("hidden"));
      panel.querySelector(".open-profile-settings").addEventListener("click", showAIProfiles);
      return;
    }
    const defaultProfile = state.aiProfiles.find((item) => item.isDefault) || state.aiProfiles[0];
    panel.innerHTML = `
      <div class="online-heading"><div><p class="detail-kicker">BEWUSSTE AI-ANALYSE</p><strong>Analyseprofil wählen</strong></div><button class="panel-close" aria-label="Schließen">×</button></div>
      <div class="ai-profile-select"><select id="analysis-profile">${state.aiProfiles.map((profile) => `<option value="${escapeHTML(profile.id)}" ${profile.id === defaultProfile.id ? "selected" : ""}>${escapeHTML(profile.name)} · ${escapeHTML(profile.model)}</option>`).join("")}</select><button class="ghost open-profile-settings">Profile</button></div>
      <p class="ai-privacy">Gesendet werden nur der Ordnername, Dateinamen und ausgewählte vorhandene Metadaten – keine Audiodaten und keine vollständigen Dateipfade.</p>
      <button class="primary compact" id="start-ai-analysis">Jetzt analysieren</button>`;
    panel.querySelector(".panel-close").addEventListener("click", () => panel.classList.add("hidden"));
    panel.querySelector(".open-profile-settings").addEventListener("click", showAIProfiles);
    panel.querySelector("#start-ai-analysis").addEventListener("click", () => analyzeWithAI(proposal, panel.querySelector("#analysis-profile").value));
  } catch (error) {
    panel.innerHTML = `<div class="online-error"><strong>AI-Profile konnten nicht geladen werden</strong><p>${escapeHTML(String(error))}</p></div>`;
  }
}

async function analyzeWithAI(proposal, profileId) {
  const panel = $("#online-panel");
  panel.innerHTML = `<div class="online-loading"><span></span>Lokale Textinformationen werden analysiert …</div>`;
  try {
    const suggestion = await api().AnalyzeWithAI(proposal.id, profileId);
    state.aiSuggestions[proposal.id] = suggestion;
    renderAISuggestion(proposal, suggestion);
  } catch (error) {
    panel.innerHTML = `<div class="online-error"><strong>AI-Analyse fehlgeschlagen</strong><p>${escapeHTML(String(error))}</p><button class="ghost retry-ai">Profil erneut wählen</button></div>`;
    panel.querySelector(".retry-ai").addEventListener("click", () => showAIChoice(proposal));
  }
}

function renderAISuggestion(proposal, suggestion) {
  const panel = $("#online-panel");
  const fields = [
    ["Titel", suggestion.title], ["Autor", suggestion.author], ["Serie", suggestion.series],
    ["Band", suggestion.seriesSequence], ["Info", suggestion.editionInfo], ["Sprecher", suggestion.narrator], ["Sprache", suggestion.language],
  ].filter(([, value]) => value);
  panel.innerHTML = `
    <div class="online-heading"><div><p class="detail-kicker">AI-VORSCHLAG · ${Math.round(suggestion.confidence * 100)} %</p><strong>Vor Übernahme prüfen</strong></div><button class="panel-close" aria-label="Schließen">×</button></div>
    <div class="ai-result"><div class="ai-result-grid">${fields.map(([label, value]) => `<div><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join("")}</div>
      ${suggestion.reasoning ? `<p class="ai-reasoning"><span>Begründung des Modells</span>${escapeHTML(suggestion.reasoning)}</p>` : ""}
      <div class="actions"><button class="ghost retry-ai">Anderes Profil</button><span class="action-spacer"></span><button class="secondary apply-ai-audible">Übernehmen & Audible prüfen</button><button class="primary compact apply-ai">Als Vorschlag übernehmen</button></div>
    </div>`;
  panel.querySelector(".panel-close").addEventListener("click", () => panel.classList.add("hidden"));
  panel.querySelector(".retry-ai").addEventListener("click", () => showAIChoice(proposal));
  panel.querySelector(".apply-ai").addEventListener("click", () => applyAISuggestion(proposal.id, false));
  panel.querySelector(".apply-ai-audible").addEventListener("click", () => applyAISuggestion(proposal.id, true));
}

async function applyAISuggestion(proposalId, searchAudible) {
  try {
    const updated = await api().ApplyAISuggestion(proposalId);
    replaceProposal(updated);
    toast("AI-Ergebnis als prüfbarer Vorschlag übernommen.");
    if (searchAudible) await searchOnline(updated, "audible");
  } catch (error) {
    toast(String(error), true);
  }
}

async function searchOnline(proposal, provider) {
  const panel = $("#online-panel");
  panel.innerHTML = `<div class="online-loading"><span></span>${provider === "audible" ? "Audible" : "Google Books"} wird durchsucht …</div>`;
  try {
    const candidates = await api().SearchOnline(proposal.id, provider, "de");
    renderCandidates(proposal, candidates || [], provider);
  } catch (error) {
    panel.innerHTML = `<div class="online-error"><strong>Suche fehlgeschlagen</strong><p>${escapeHTML(String(error))}</p><button class="ghost retry-online">Anbieterauswahl</button></div>`;
    panel.querySelector(".retry-online").addEventListener("click", () => showProviderChoice(proposal));
  }
}

function renderCandidates(proposal, candidates, provider) {
  const panel = $("#online-panel");
  panel.innerHTML = `
    <div class="online-heading">
      <div><p class="detail-kicker">${provider === "audible" ? "AUDIBLE DEUTSCHLAND" : "GOOGLE BOOKS"}</p><strong>${candidates.length} Treffer</strong></div>
      <button class="panel-close" aria-label="Schließen">×</button>
    </div>
    ${candidates.length ? `<div class="candidate-list">${candidates.map(candidateCard).join("")}</div>` : `<div class="no-candidates">Keine passenden Treffer gefunden.</div>`}
    <button class="ghost change-provider">Anderen Anbieter wählen</button>
  `;
  panel.querySelector(".panel-close").addEventListener("click", () => panel.classList.add("hidden"));
  panel.querySelector(".change-provider").addEventListener("click", () => showProviderChoice(proposal));
  panel.querySelectorAll(".apply-candidate").forEach((button) => {
    button.addEventListener("click", () => applyCandidate(proposal.id, button.dataset.candidateId));
  });
}

function candidateCard(candidate) {
  const details = [
    candidate.series ? `${candidate.series}${candidate.seriesSequence ? ` · Band ${candidate.seriesSequence}` : ""}` : "",
    candidate.narrator ? `Sprecher: ${candidate.narrator}` : "",
    candidate.durationMinutes ? `Laufzeit: ${Math.floor(candidate.durationMinutes / 60)} Std. ${candidate.durationMinutes % 60} Min.` : "",
    candidate.isbn ? `ISBN: ${candidate.isbn}` : candidate.asin ? `ASIN: ${candidate.asin}` : "",
  ].filter(Boolean);
  return `
    <article class="candidate-card">
      <div class="candidate-cover">${candidate.coverUrl ? `<img src="${escapeHTML(candidate.coverUrl)}" alt="" referrerpolicy="no-referrer" />` : `<span>${candidate.provider === "audible" ? "A" : "G"}</span>`}</div>
      <div class="candidate-copy">
        <div class="candidate-score">${Math.round(candidate.confidence * 100)} % Übereinstimmung</div>
        <h4>${escapeHTML(candidate.title)}</h4>
        <p>${escapeHTML(candidate.author || "Unbekannter Autor")}</p>
        <small>${details.map(escapeHTML).join("<br>")}</small>
      </div>
      <button class="secondary apply-candidate" data-candidate-id="${escapeHTML(candidate.id)}">Übernehmen</button>
    </article>
  `;
}

async function applyCandidate(proposalId, candidateId) {
  try {
    const updated = await api().ApplyOnlineCandidate(proposalId, candidateId);
    replaceProposal(updated);
    toast("Online-Treffer als neuer Vorschlag übernommen. Bitte noch bestätigen.");
  } catch (error) {
    toast(String(error), true);
    render();
  }
}

function field(label, name, value, source, required = false) {
  return `<label><span>${label}</span><input name="${name}" value="${escapeHTML(value || "")}" ${required ? "required" : ""}/><small>${escapeHTML(source)}</small></label>`;
}

function formMetadata(previous) {
  const data = new FormData($("#metadata-form"));
  return {
    title: data.get("title"), author: data.get("author"), series: data.get("series"),
    seriesSequence: data.get("seriesSequence"), editionInfo: data.get("editionInfo"),
    narrator: data.get("narrator"), language: data.get("language"),
    asin: data.get("asin"), isbn: data.get("isbn"), evidence: previous.evidence || {},
  };
}

async function saveProposal(proposal, confirm) {
  try {
    let updated = await api().UpdateProposal(proposal.id, formMetadata(proposal.metadata));
    if (confirm) updated = await api().SetProposalStatus(proposal.id, "confirmed");
    replaceProposal(updated);
    toast(confirm ? "Vorschlag bestätigt." : "Änderungen gespeichert.");
  } catch (error) {
    toast(String(error), true);
    render();
  }
}

async function setStatus(id, status) {
  try {
    replaceProposal(await api().SetProposalStatus(id, status));
  } catch (error) {
    toast(String(error), true);
  }
}

function replaceProposal(updated) {
  const index = state.proposals.findIndex((item) => item.id === updated.id);
  state.proposals[index] = updated;
  render();
}

async function buildPlan() {
  const target = $("#target").value.trim();
  if (!target) return toast("Bitte einen Zielordner auswählen.", true);
  try {
    savePreferences();
    const options = planOptions();
    const plan = await api().BuildPlan(target, options);
    state.lastPlan = plan;
    state.lastPlanOptions = options;
    state.lastExecution = null;
    const output = $("#plan-output");
    output.innerHTML = `
      <div class="plan-summary ${plan.executable ? "ready" : "blocked"}">
        <strong>${plan.executable ? "Plan ist konfliktfrei" : "Plan benötigt Aufmerksamkeit"}</strong>
        <span>${plan.operations.length} Operationen · ${formatBytes(plan.totalBytes)}</span>
      </div>
      ${plan.warnings?.length ? `<div class="warning-list">${plan.warnings.map(escapeHTML).join("<br>")}</div>` : ""}
      <div class="operation-list">${plan.operations.map(operationRow).join("")}</div>
      ${plan.executable ? `
        <div class="execution-box">
          <label><input type="checkbox" id="execution-consent" /> Ich habe Quelle und Ziel geprüft und möchte die angezeigten Dateien verschieben.</label>
          <button type="button" class="primary compact" id="execute-plan" disabled>Dateien verschieben</button>
          <p class="execution-status" id="execution-status" aria-live="polite">Nach Aktivierung der Checkbox wird das Verschieben freigegeben.</p>
          <div class="execution-progress hidden" id="execution-progress" role="progressbar" aria-label="Verschiebefortschritt" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"><span id="execution-progress-fill"></span></div>
        </div>
        <p class="dry-run-note">Die Vorschau verändert noch nichts. Erst „Dateien verschieben“ startet die journalisierte Übertragung. Die Quelle wird erst nach vollständiger Kopie und erfolgreichem SHA-256-Vergleich entfernt.</p>
      ` : `<p class="dry-run-note">Konflikte müssen vor der Ausführung behoben werden.</p>`}
    `;
    if (plan.executable) {
      const consent = $("#execution-consent");
      const execute = $("#execute-plan");
      consent.addEventListener("change", () => {
        execute.disabled = !consent.checked;
        $("#execution-status").textContent = consent.checked ? `${plan.operations.length} Operationen sind zum Verschieben freigegeben.` : "Nach Aktivierung der Checkbox wird das Verschieben freigegeben.";
      });
      execute.addEventListener("click", executePlan);
    }
  } catch (error) {
    toast(String(error), true);
  }
}

function planOptions() {
  return {
    moveEbooks: $("#move-ebooks").checked,
    cleanupSidecars: $("#cleanup-sidecars").checked,
    bookNumberWidth: selectedNumberWidth("#book-number-width"),
    trackNumberWidth: selectedNumberWidth("#track-number-width"),
    audioFileNaming: $("#audio-file-naming")?.value || "source_title",
  };
}

function operationRow(operation) {
  const label = operation.action === "remove" ? "Entfernen (Undo-fähig)" : operation.target;
  const category = { audio: "Audio", ebook: "E-Book", sidecar: "Bereinigung" }[operation.category] || "Datei";
  return `<div><i class="operation-category ${escapeHTML(operation.category)}">${category}</i><span>${escapeHTML(operation.source)}</span><b>→</b><strong>${escapeHTML(label)}</strong></div>`;
}

async function executePlan() {
  const target = state.lastPlan?.targetRoot || "";
  const count = state.lastPlan?.operations.length || 0;
  if (!count) return toast("Bitte zuerst einen ausführbaren Plan erstellen.", true);
  const button = $("#execute-plan");
  const status = $("#execution-status");
  if (!button || button.disabled) return;
  button.disabled = true;
  button.textContent = "Dateien werden verschoben …";
  status.textContent = `Übertragung gestartet: 0 von ${count} Operationen abgeschlossen. Bitte die App geöffnet lassen.`;
  status.className = "execution-status running";
  state.executionInProgress = true;
  showExecutionProgress(0);
  await new Promise((resolve) => requestAnimationFrame(() => resolve()));
  try {
    const result = await api().ExecutePlan(target, state.lastPlanOptions || planOptions());
    state.lastExecution = result;
    renderExecutionResult(result);
    if (result.status === "completed") {
      state.proposals.filter((item) => item.status === "confirmed").forEach((item) => { item.status = "imported"; });
      renderList();
      toast("Dateien wurden erfolgreich verschoben.");
    } else {
      if (result.completed > 0) {
        state.proposals.filter((item) => item.status === "confirmed").forEach((item) => { item.status = "error"; });
        renderList();
      }
      toast(result.error || "Das Verschieben wurde nicht vollständig abgeschlossen.", true);
    }
  } catch (error) {
    toast(String(error), true);
    button.disabled = false;
    button.textContent = "Erneut versuchen";
    status.textContent = `Verschieben nicht gestartet oder unterbrochen: ${String(error)}`;
    status.className = "execution-status error";
    if (isSourceError(error)) addRescanButton(status);
  } finally {
    state.executionInProgress = false;
  }
}

function handleExecutionProgress(progress) {
  if (!state.executionInProgress) return;
  const completed = Number(progress?.completed) || 0;
  const total = Number(progress?.total) || state.lastPlan?.operations.length || 0;
  const completedBytes = Number(progress?.completedBytes) || 0;
  const totalBytes = Number(progress?.totalBytes) || state.lastPlan?.totalBytes || 0;
  const fraction = totalBytes > 0 ? completedBytes / totalBytes : total > 0 ? completed / total : 0;
  const percent = progress?.status === "completed" ? 100 : Math.max(0, Math.min(100, Math.round(fraction * 100)));
  const status = $("#execution-status");
  const button = $("#execute-plan");
  if (!status || !button) return;
  const currentName = pathBaseName(progress?.currentSource || "");
  if (progress?.status === "checking") {
    status.textContent = `Alle ${total} Quelldateien werden vorab geprüft …`;
  } else {
    const bytes = totalBytes > 0 ? ` · ${formatBytes(completedBytes)} von ${formatBytes(totalBytes)}` : "";
    const current = currentName && completed < total ? ` · Aktuell: ${currentName}` : "";
    status.textContent = `${completed} von ${total} Operationen abgeschlossen${bytes}${current}`;
  }
  status.className = "execution-status running";
  button.textContent = `${percent} % · Dateien werden verschoben …`;
  showExecutionProgress(percent);
}

function showExecutionProgress(percent) {
  const progress = $("#execution-progress");
  const fill = $("#execution-progress-fill");
  if (!progress || !fill) return;
  progress.classList.remove("hidden");
  progress.setAttribute("aria-valuenow", String(percent));
  fill.style.width = `${percent}%`;
}

function pathBaseName(path) {
  return String(path || "").replaceAll("\\", "/").split("/").filter(Boolean).pop() || "";
}

function renderExecutionResult(result) {
  const output = $("#plan-output");
  const successful = result.status === "completed";
  const undone = result.status === "undone";
  const canUndo = !undone && result.completed > 0 && result.journalId;
  const needsRescan = isSourceError(result.error);
  output.innerHTML = `
    <div class="plan-summary ${successful || undone ? "ready" : "blocked"}">
      <strong>${successful ? "Dateien verschoben" : undone ? "Verschieben rückgängig gemacht" : "Verschieben unterbrochen"}</strong>
      <span>${result.completed} von ${result.total} Operationen · ${formatBytes(result.totalBytes)}</span>
    </div>
    ${result.error ? `<div class="warning-list">${escapeHTML(result.error)}</div>` : ""}
    ${result.warnings?.length ? `<div class="notice execution-notice">${result.warnings.map(escapeHTML).join("<br>")}</div>` : ""}
    ${needsRescan ? `<div class="source-recovery"><span>Die Quelle hat sich seit dem Scan geändert oder verwendet eine nicht mehr auflösbare Pfadschreibweise.</span><button type="button" class="secondary" id="rescan-source">Quelle neu scannen</button></div>` : ""}
    <p class="journal-id">Journal: ${escapeHTML(result.journalId)}</p>
    ${canUndo ? `<div class="undo-box"><span>Undo ist möglich, solange keine Zieldatei verändert wurde.</span><button type="button" class="ghost danger" id="undo-execution">Verschieben rückgängig machen</button></div>` : ""}
  `;
  $("#undo-execution")?.addEventListener("click", undoExecution);
  $("#rescan-source")?.addEventListener("click", scan);
}

function isSourceError(error) {
  const message = String(error || "").toLocaleLowerCase();
  return message.includes("quelle") && (message.includes("nicht mehr") || message.includes("no such file") || message.includes("lstat") || message.includes("neu scannen"));
}

function addRescanButton(status) {
  if (status.parentElement.querySelector(".rescan-inline")) return;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "ghost rescan-inline";
  button.textContent = "Quelle neu scannen";
  button.addEventListener("click", scan);
  status.after(button);
}

async function undoExecution() {
  const result = state.lastExecution;
  if (!result?.journalId) return;
  const button = $("#undo-execution");
  button.disabled = true;
  button.textContent = "Dateien werden zurückverschoben …";
  try {
    const undone = await api().UndoExecution(result.journalId);
    state.lastExecution = undone;
    renderExecutionResult(undone);
    if (undone.status === "undone") {
      state.proposals.filter((item) => item.status === "imported" || item.status === "error").forEach((item) => { item.status = "confirmed"; });
      renderList();
      toast("Das Verschieben wurde rückgängig gemacht.");
    } else {
      toast(undone.error || "Undo konnte nicht abgeschlossen werden.", true);
    }
  } catch (error) {
    toast(String(error), true);
    button.disabled = false;
    button.textContent = "Verschieben rückgängig machen";
  }
}

const aiProviderDefaults = {
  ollama: "http://127.0.0.1:11434",
  lmstudio: "http://127.0.0.1:1234/v1",
  openai: "https://api.openai.com/v1",
  openrouter: "https://openrouter.ai/api/v1",
  groq: "https://api.groq.com/openai/v1",
  openai_compatible: "",
};

async function showAIProfiles() {
  $("#ai-profile-overlay").classList.remove("hidden");
  try {
    state.aiProfiles = await api().GetAIProfiles();
    renderAIProfileList();
    editAIProfile(state.aiProfiles.find((item) => item.isDefault) || state.aiProfiles[0] || null);
  } catch (error) {
    $("#ai-profile-list").innerHTML = `<div class="online-error">${escapeHTML(String(error))}</div>`;
  }
}

function renderAIProfileList(activeId = document.querySelector('#ai-profile-form [name="id"]')?.value) {
  const list = $("#ai-profile-list");
  list.innerHTML = `<button class="secondary new-profile">+ Neues Profil</button>${state.aiProfiles.map((profile) => `
    <button class="ai-profile-item ${profile.id === activeId ? "active" : ""}" data-profile-id="${escapeHTML(profile.id)}">
      ${profile.isDefault ? `<span class="ai-profile-default">Standard</span>` : ""}<strong>${escapeHTML(profile.name)}</strong><small>${escapeHTML(profile.provider)} · ${escapeHTML(profile.model)}</small>
    </button>`).join("")}`;
  list.querySelector(".new-profile").addEventListener("click", () => editAIProfile(null));
  list.querySelectorAll(".ai-profile-item").forEach((button) => button.addEventListener("click", () => editAIProfile(state.aiProfiles.find((item) => item.id === button.dataset.profileId))));
}

function editAIProfile(profile) {
  const form = $("#ai-profile-form");
  form.reset();
  form.elements.id.value = profile?.id || "";
  form.elements.name.value = profile?.name || "";
  form.elements.provider.value = profile?.provider || "ollama";
  form.elements.baseUrl.value = profile?.baseUrl || aiProviderDefaults[form.elements.provider.value];
  form.elements.model.value = profile?.model || "";
  form.elements.isDefault.checked = profile?.isDefault || !state.aiProfiles.length;
  form.elements.clearApiKey.checked = false;
  $("#ai-key-state").textContent = profile?.hasApiKey ? "Ein verschlüsselter Schlüssel ist gespeichert." : "Kein Schlüssel gespeichert; für lokale Anbieter meist nicht erforderlich.";
  $("#delete-ai-profile").classList.toggle("hidden", !profile);
  renderAIProfileList(profile?.id || "");
}

async function saveAIProfile(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const input = {
    id: form.elements.id.value,
    name: form.elements.name.value,
    provider: form.elements.provider.value,
    baseUrl: form.elements.baseUrl.value,
    model: form.elements.model.value,
    apiKey: form.elements.apiKey.value,
    clearApiKey: form.elements.clearApiKey.checked,
    isDefault: form.elements.isDefault.checked,
  };
  try {
    const saved = await api().SaveAIProfile(input);
    state.aiProfiles = await api().GetAIProfiles();
    editAIProfile(saved);
    form.elements.apiKey.value = "";
    toast("AI-Profil lokal gespeichert.");
  } catch (error) {
    toast(String(error), true);
  }
}

async function testAIProfile() {
  const id = document.querySelector('#ai-profile-form [name="id"]').value;
  if (!id) return toast("Bitte das Profil vor dem Verbindungstest speichern.", true);
  const button = $("#test-ai-profile");
  button.disabled = true;
  button.textContent = "Test läuft …";
  try {
    await api().TestAIProfile(id);
    toast("AI-Verbindung erfolgreich getestet.");
  } catch (error) {
    toast(String(error), true);
  } finally {
    button.disabled = false;
    button.textContent = "Verbindung testen";
  }
}

async function deleteAIProfile() {
  const id = document.querySelector('#ai-profile-form [name="id"]').value;
  if (!id || !window.confirm("Dieses AI-Profil einschließlich des gespeicherten Schlüssels löschen?")) return;
  try {
    await api().DeleteAIProfile(id);
    state.aiProfiles = await api().GetAIProfiles();
    renderAIProfileList();
    editAIProfile(state.aiProfiles[0] || null);
    toast("AI-Profil gelöscht.");
  } catch (error) {
    toast(String(error), true);
  }
}

restorePreferences();

$("#choose-source").addEventListener("click", () => chooseDirectory($("#source"), "Quellordner auswählen"));
$("#choose-target").addEventListener("click", () => chooseDirectory($("#target"), "Zielordner auswählen"));
$("#scan").addEventListener("click", scan);
$("#build-plan").addEventListener("click", buildPlan);
$("#open-log").addEventListener("click", showSessionLog);
$("#open-ai-profiles").addEventListener("click", showAIProfiles);
$("#close-ai-profiles").addEventListener("click", () => $("#ai-profile-overlay").classList.add("hidden"));

window.runtime?.EventsOn?.("execution:progress", handleExecutionProgress);
$("#ai-profile-form").addEventListener("submit", saveAIProfile);
$("#test-ai-profile").addEventListener("click", testAIProfile);
$("#delete-ai-profile").addEventListener("click", deleteAIProfile);
$("#ai-profile-provider").addEventListener("change", (event) => {
  document.querySelector('#ai-profile-form [name="baseUrl"]').value = aiProviderDefaults[event.target.value] || "";
});
$("#refresh-log").addEventListener("click", showSessionLog);
$("#close-log").addEventListener("click", () => $("#log-overlay").classList.add("hidden"));
[$("#book-number-width"), $("#track-number-width"), $("#audio-file-naming"), $("#move-ebooks"), $("#cleanup-sidecars")].forEach((input) => {
  input.addEventListener("change", () => {
    savePreferences();
    state.lastPlan = null;
    render();
  });
});

async function handleDroppedPaths(paths) {
  const usable = (paths || []).filter(Boolean);
  if (!usable.length) return;
  if (usable.length > 1) toast("Mehrere Ablagen erkannt; der erste Ordner wird verwendet.");
  $("#source").value = usable[0];
  await scan();
}

function setupFileDrop() {
  const zone = $("#source-drop-zone");
  if (window.runtime?.OnFileDrop) {
    window.runtime.OnFileDrop((_x, _y, paths) => handleDroppedPaths(paths), true);
  }
  zone.addEventListener("dragover", (event) => {
    event.preventDefault();
    zone.classList.add("drag-active");
  });
  zone.addEventListener("dragleave", () => zone.classList.remove("drag-active"));
  zone.addEventListener("drop", (event) => {
    event.preventDefault();
    zone.classList.remove("drag-active");
    if (window.runtime?.OnFileDrop) return;
    const paths = [...(event.dataTransfer?.files || [])].map((file) => file.path).filter(Boolean);
    if (paths.length) handleDroppedPaths(paths);
    else toast("Die Browser-Vorschau kann keine lokalen Ordnerpfade lesen. Drag & Drop funktioniert in der Desktop-App.");
  });
}

setupFileDrop();
$("#log-overlay").addEventListener("click", (event) => {
  if (event.target === $("#log-overlay")) $("#log-overlay").classList.add("hidden");
});
$("#ai-profile-overlay").addEventListener("click", (event) => {
  if (event.target === $("#ai-profile-overlay")) $("#ai-profile-overlay").classList.add("hidden");
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    $("#log-overlay").classList.add("hidden");
    $("#ai-profile-overlay").classList.add("hidden");
  }
});
