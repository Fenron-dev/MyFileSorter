const state = {
  proposals: [], selectedId: null, lastPlan: null, lastPlanOptions: null, lastExecution: null,
  aiProfiles: [], aiSuggestions: {}, executionInProgress: false, scanInProgress: false,
  dirtyProposalId: null, dirtyRevision: 0, bulkInProgress: false, cancellationRequested: false,
  reviewLimit: 200, mergeSelection: new Set(), trackDrafts: new Map(), trackMutationInProgress: false,
  scanProgress: { discovered: 0, inspected: 0, phase: "", currentPath: "" },
  logSessionId: "",
};
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
    { path: "/Vorschau/Unsortiert/name_des_windes_01.m4b", name: "name_des_windes_01.m4b", extension: ".m4b", size: 734003200, track: 1, disc: 1, targetTitle: "Der Name des Windes – Teil 1", excluded: false },
    { path: "/Vorschau/Unsortiert/name_des_windes_02.m4b", name: "name_des_windes_02.m4b", extension: ".m4b", size: 681574400, track: 2, disc: 1, targetTitle: "", excluded: false },
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
const previewImportHistory = [{
  journalId: "browser-preview-journal", status: "completed", targetRoot: "/Vorschau/Audiobookshelf",
  createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(), completed: 3, total: 3,
  totalBytes: 1418723328, error: "", canUndo: true,
}];

const browserPreviewAPI = {
  async SelectDirectory(title) {
    return title.includes("Ziel") ? "/Vorschau/Audiobookshelf" : "/Vorschau/Unsortiert";
  },
  async Scan(source) {
    const proposal = structuredClone(previewProposal);
    proposal.sourceRoot = source;
    handleScanProgress({ phase: "discovering", discovered: 2, inspected: 0, currentPath: source });
    handleScanProgress({ phase: "inspecting", discovered: 2, inspected: 1, currentPath: proposal.files[0].path });
    handleScanProgress({ phase: "completed", discovered: 2, inspected: 2, currentPath: proposal.files[1].path });
    return {
      source,
      proposals: [proposal],
      summary: { books: 1, files: 2, ebooks: 1, sidecars: 1, bytes: 1415577600, metadataAvailable: true },
      globalNotes: ["Browser-Vorschau mit Beispieldaten – es werden keine lokalen Dateien gelesen."],
    };
  },
  async GetProposals() { return structuredClone(state.proposals); },
  async UpdateProposal(id, metadata) {
    const proposal = state.proposals.find((item) => item.id === id);
    return { ...proposal, metadata, status: "review_required" };
  },
  async SetProposalStatus(id, status) {
    const proposal = state.proposals.find((item) => item.id === id);
    return { ...proposal, status };
  },
  async BulkSetProposalStatus(ids, status) {
    const selected = new Set(ids);
    const updated = state.proposals.filter((item) => selected.has(item.id)).map((item) => ({ ...item, status }));
    for (const proposal of updated) storeProposal(proposal);
    return structuredClone(updated);
  },
  async SearchOnline(id, provider) {
    return structuredClone(previewCandidates[provider] || []);
  },
  async SearchOnlineWithQuery(id, provider, query) {
    void query;
    return this.SearchOnline(id, provider);
  },
  async UpdateTrack(proposalId, update) {
    const proposal = structuredClone(state.proposals.find((item) => item.id === proposalId));
    const file = proposal?.files?.find((item) => item.path === update.path);
    if (!file) throw new Error("Track wurde im Vorschlag nicht gefunden");
    file.targetTitle = String(update.targetTitle || "").trim();
    file.track = Number(update.track) || 0;
    file.disc = Number(update.disc) || 0;
    file.excluded = Boolean(update.excluded);
    proposal.status = "review_required";
    storeProposal(proposal);
    return structuredClone(proposal);
  },
  async ReorderTracks(proposalId, orderedPaths) {
    const proposal = structuredClone(state.proposals.find((item) => item.id === proposalId));
    const byPath = new Map((proposal?.files || []).map((file) => [file.path, file]));
    if (orderedPaths.length !== byPath.size || orderedPaths.some((path) => !byPath.has(path))) {
      throw new Error("Die neue Reihenfolge muss jeden Track genau einmal enthalten");
    }
    proposal.files = orderedPaths.map((path) => byPath.get(path));
    proposal.status = "review_required";
    storeProposal(proposal);
    return structuredClone(proposal);
  },
  async MergeProposals(ids) {
    const selected = new Set(ids);
    const items = state.proposals.filter((item) => selected.has(item.id));
    if (items.length < 2) throw new Error("Mindestens zwei Vorschläge auswählen");
    const merged = structuredClone(items[0]);
    merged.id = `browser-merged-${Date.now()}`;
    merged.files = items.flatMap((item) => structuredClone(item.files || []));
    merged.companions = items.flatMap((item) => structuredClone(item.companions || []));
    merged.status = "review_required";
    merged.warnings = [...new Set([...(merged.warnings || []), "Vorschläge wurden manuell zusammengeführt; Trackreihenfolge und Metadaten bitte prüfen."])];
    const insertAt = state.proposals.findIndex((item) => selected.has(item.id));
    state.proposals = state.proposals.filter((item) => !selected.has(item.id));
    state.proposals.splice(Math.max(0, insertAt), 0, merged);
    return structuredClone(merged);
  },
  async SplitProposal(proposalId, selectedPaths) {
    const selected = new Set(selectedPaths);
    const proposal = state.proposals.find((item) => item.id === proposalId);
    const left = (proposal?.files || []).filter((file) => !selected.has(file.path));
    const right = (proposal?.files || []).filter((file) => selected.has(file.path));
    if (!left.length || !right.length || right.length !== selected.size) throw new Error("Auf beiden Seiten müssen Tracks verbleiben");
    proposal.files = left;
    proposal.status = "review_required";
    const created = structuredClone(proposal);
    created.id = `browser-split-${Date.now()}`;
    created.files = right;
    created.companions = [];
    created.warnings = [...new Set([...(created.warnings || []), "Manuell abgetrennter Vorschlag; Metadaten bitte prüfen."])];
    const index = state.proposals.findIndex((item) => item.id === proposalId);
    state.proposals.splice(index + 1, 0, created);
    return structuredClone([proposal, created]);
  },
  async UpdateCompanion(proposalId, path, kind) {
    const proposal = structuredClone(state.proposals.find((item) => item.id === proposalId));
    const companion = proposal?.companions?.find((item) => item.path === path);
    if (!companion) throw new Error("Begleitdatei wurde im Vorschlag nicht gefunden");
    companion.kind = kind;
    proposal.status = "review_required";
    storeProposal(proposal);
    return structuredClone(proposal);
  },
  async MoveCompanions(sourceProposalId, targetProposalId, paths) {
    const source = state.proposals.find((item) => item.id === sourceProposalId);
    const target = state.proposals.find((item) => item.id === targetProposalId);
    if (!source || !target) throw new Error("Quell- oder Zielvorschlag wurde nicht gefunden");
    const selected = new Set(paths);
    const moving = source.companions.filter((item) => selected.has(item.path));
    source.companions = source.companions.filter((item) => !selected.has(item.path));
    target.companions = [...(target.companions || []), ...moving];
    source.status = "review_required";
    target.status = "review_required";
    return structuredClone([source, target]);
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
    const audio = confirmed.flatMap((proposal) => proposal.files.filter((file) => !file.excluded).map((file, index) => ({
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
      planId: `browser-preview-plan-${Date.now()}`,
      digest: "browser-preview",
      revision: Date.now(),
      targetRoot: target,
      operations,
      totalBytes: operations.reduce((total, item) => total + item.size, 0),
      warnings: operations.length ? [] : ["Bitte zuerst den Beispielvorschlag bestätigen."],
      executable: operations.length > 0,
    };
  },
  async ExecutePlan(planId) {
    if (!planId || planId !== state.lastPlan?.planId) throw new Error("Die Vorschau ist nicht mehr aktuell.");
    state.cancellationRequested = false;
    const operations = state.lastPlan?.operations || [];
    let completedBytes = 0;
    handleExecutionProgress({ status: "checking", completed: 0, total: operations.length, completedBytes: 0, totalBytes: state.lastPlan?.totalBytes || 0 });
    for (let index = 0; index < operations.length; index += 1) {
      await new Promise((resolve) => setTimeout(resolve, 180));
      if (state.cancellationRequested) throw new Error("Browser-Vorschau wurde abgebrochen.");
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
  async CancelActiveJob() { state.cancellationRequested = true; },
  async UndoExecution(journalId) {
    const run = previewImportHistory.find((item) => item.journalId === journalId);
    if (run) {
      run.status = "undone";
      run.completed = 0;
      run.canUndo = false;
      run.updatedAt = new Date().toISOString();
    }
    return {
      journalId, status: "undone", completed: 0,
      total: state.lastPlan?.operations.length || 0, totalBytes: 0,
      warnings: ["Browser-Vorschau: Es wurden keine Dateien verändert."],
    };
  },
  async GetImportHistory() { return structuredClone(previewImportHistory); },
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
  async GetLogSessions() {
    return [
      { sessionId: "browser-preview", modifiedAt: new Date().toISOString(), size: 2048, current: true },
      { sessionId: "browser-preview-previous", modifiedAt: new Date(Date.now() - 86400000).toISOString(), size: 1536, current: false },
    ];
  },
  async GetLogSession(sessionId) {
    const snapshot = await this.GetSessionLog();
    snapshot.sessionId = sessionId;
    snapshot.filePath = `/Vorschau/MyFileSorter/logs/session-${sessionId}.jsonl`;
    return snapshot;
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

function safeHTTPSURL(value = "") {
  try {
    const parsed = new URL(String(value));
    return parsed.protocol === "https:" ? parsed.href : "";
  } catch (_error) {
    return "";
  }
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

function statusClass(status) {
  return ["review_required", "confirmed", "excluded", "imported", "conflict", "error"].includes(status) ? status : "unknown";
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
    if (["error", "skip_identical"].includes(preferences.existingFilePolicy)) $("#existing-file-policy").value = preferences.existingFilePolicy;
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

function effectiveTrackNumbers(files) {
  const detected = files.map((file) => Number(file.track) || 0);
  const useDetected = detected.every((number) => number > 0) && new Set(detected).size === detected.length;
  return useDetected ? detected : files.map((_file, index) => index + 1);
}

function automaticTrackWidth(files) {
	const maximum = Math.max(files.length, ...effectiveTrackNumbers(files), 1);
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
  if (file.excluded) return "Wird nicht übertragen";
  const includedFiles = (proposal.files || []).filter((item) => !item.excluded);
  const includedIndex = Math.max(0, includedFiles.findIndex((item) => item.path === file.path));
  const requested = Number(options.trackNumberWidth ?? 2);
  const width = requested < 0 ? automaticTrackWidth(includedFiles) : Math.max(1, requested || 2);
  const number = effectiveTrackNumbers(includedFiles)[includedIndex] || includedIndex + 1;
  let title = proposal.metadata.title;
  if (options.audioFileNaming === "source_title") {
    const extensionPattern = new RegExp(`${String(file.extension || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`, "i");
    let sourceTitle = String(file.name || "").replace(extensionPattern, "");
    const match = sourceTitle.match(/^\s*(\d+)(?:\s*[-._]+\s*|\s+)(.*)$/) || sourceTitle.match(/^\s*(\d+)\s*$/)?.concat("");
    if (match) {
      const parsedPrefix = Number.parseInt(match[1], 10);
      const embeddedTrack = Number(file.track) || 0;
      if (parsedPrefix > 0 && (parsedPrefix === embeddedTrack || parsedPrefix === includedIndex + 1 || (includedFiles.length > 1 && parsedPrefix <= includedFiles.length))) {
        sourceTitle = match[2];
      }
    }
    title = sourceTitle.replaceAll("_", " ").replaceAll(".", " ").replace(/\s+/g, " ").replace(/^[- ]+|[- ]+$/g, "") || "Track";
  }
  if (String(file.targetTitle || "").trim()) {
    title = String(file.targetTitle).trim();
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

function invalidatePlan(reason = "Die Vorschau wurde durch eine Änderung ungültig. Bitte neu erstellen.", quiet = false) {
  const hadPlan = Boolean(state.lastPlan);
  state.lastPlan = null;
  state.lastPlanOptions = null;
  const output = $("#plan-output");
  if (!output) return;
  if (!quiet && (hadPlan || output.childElementCount)) {
    output.innerHTML = `<div class="plan-invalidated" role="status">${escapeHTML(reason)}</div>`;
  } else if (quiet) {
    output.innerHTML = "";
  }
}

function proposalIsValid(proposal) {
  const metadata = proposal?.metadata || {};
  return Boolean(String(metadata.title || "").trim()
    && String(metadata.author || "").trim()
    && (!String(metadata.series || "").trim() || String(metadata.seriesSequence || "").trim()));
}

function proposalIsSafe(proposal) {
  return proposalIsValid(proposal)
    && !["imported", "conflict", "error"].includes(proposal.status)
    && !(proposal.warnings || []).length
    && Number(proposal.confidence || 0) >= 0.75;
}

function filteredProposals() {
  const query = String($("#review-search")?.value || "").trim().toLocaleLowerCase();
  const status = $("#review-status")?.value || "all";
  const sort = $("#review-sort")?.value || "source";
  const matches = state.proposals.filter((proposal) => {
    if (status !== "all" && proposal.status !== status) return false;
    if (!query) return true;
    const metadata = proposal.metadata || {};
    const haystack = [metadata.title, metadata.author, metadata.series, metadata.seriesSequence,
      metadata.narrator, metadata.asin, metadata.isbn, proposal.groupPath]
      .filter(Boolean).join(" ").toLocaleLowerCase();
    return haystack.includes(query);
  });
  const collator = new Intl.Collator("de", { numeric: true, sensitivity: "base" });
  if (sort === "title") matches.sort((left, right) => collator.compare(left.metadata.title, right.metadata.title));
  if (sort === "author") matches.sort((left, right) => collator.compare(left.metadata.author, right.metadata.author));
  if (sort === "confidence_asc") matches.sort((left, right) => Number(left.confidence || 0) - Number(right.confidence || 0));
  if (sort === "status") matches.sort((left, right) => collator.compare(left.status, right.status));
  return matches;
}

function pruneMergeSelection() {
  const current = new Set(state.proposals.filter((proposal) => proposal.status !== "imported").map((proposal) => proposal.id));
  for (const id of state.mergeSelection) {
    if (!current.has(id)) state.mergeSelection.delete(id);
  }
}

function updateMergeControls() {
  pruneMergeSelection();
  const count = state.mergeSelection.size;
  const label = $("#merge-selected-count");
  if (label) label.textContent = `${count} zum Verbinden markiert`;
  const merge = $("#merge-selected");
  const clear = $("#clear-merge-selection");
  if (merge) merge.disabled = state.bulkInProgress || count < 2;
  if (clear) clear.disabled = state.bulkInProgress || count === 0;
}

function updateReviewCounts(visible = filteredProposals(), rendered = Math.min(visible.length, state.reviewLimit)) {
  const selected = state.proposals.filter((item) => item.status === "confirmed").length;
  const unresolved = state.proposals.filter((item) => item.status === "review_required").length;
  const element = $("#review-counts");
  if (element) element.textContent = `${visible.length} von ${state.proposals.length} sichtbar${rendered < visible.length ? ` · ${rendered} geladen` : ""} · ${selected} ausgewählt · ${unresolved} zu prüfen`;
  for (const selector of ["#select-safe-visible", "#select-visible", "#deselect-visible"]) {
    const button = $(selector);
    if (button) button.disabled = state.bulkInProgress || !visible.length;
  }
  if ($("#next-review")) $("#next-review").disabled = state.bulkInProgress || !unresolved;
  updateMergeControls();
}

function updateScanSummary(proposals = state.proposals) {
  const summary = proposals.reduce((total, proposal) => {
    total.books += 1;
    total.files += (proposal.files || []).length;
    total.bytes += (proposal.files || []).reduce((sum, file) => sum + Number(file.size || 0), 0);
    total.ebooks += (proposal.companions || []).filter((file) => file.kind === "ebook").length;
    total.sidecars += (proposal.companions || []).filter((file) => file.kind === "discard").length;
    return total;
  }, { books: 0, files: 0, bytes: 0, ebooks: 0, sidecars: 0 });
  const extras = [
    summary.ebooks ? `${summary.ebooks} E-Book${summary.ebooks === 1 ? "" : "s"}` : "",
    summary.sidecars ? `${summary.sidecars} Begleitdatei${summary.sidecars === 1 ? "" : "en"}` : "",
  ].filter(Boolean).join(" · ");
  $("#summary").innerHTML = `<strong>${summary.books}</strong> Bücher · <strong>${summary.files}</strong> Audiodateien · ${formatBytes(summary.bytes)}${extras ? ` · ${extras}` : ""}`;
}

async function refreshProposalsFromBackend() {
  try {
    const proposals = await api().GetProposals();
    if (!Array.isArray(proposals)) return false;
    state.proposals = proposals;
    pruneMergeSelection();
    if (!proposals.some((item) => item.id === state.selectedId)) state.selectedId = proposals[0]?.id || null;
    updateScanSummary();
    render();
    return true;
  } catch (_error) {
    return false;
  }
}

function storeProposal(updated) {
  const index = state.proposals.findIndex((item) => item.id === updated.id);
  if (index >= 0) state.proposals[index] = updated;
}

function markProposalDirty() {
  if (!state.selectedId) return;
  invalidatePlan();
  state.dirtyProposalId = state.selectedId;
  state.dirtyRevision += 1;
  $("#save")?.classList.add("dirty");
  if ($("#save")) $("#save").textContent = "Ungespeicherte Änderungen";
  clearTimeout(markProposalDirty.timer);
  const revision = state.dirtyRevision;
  markProposalDirty.timer = setTimeout(() => {
    if (state.dirtyProposalId && state.dirtyRevision === revision && !state.scanInProgress && !state.executionInProgress) {
      saveDirtyDraft(false);
    }
  }, 1400);
}

function trackDraftsFor(proposalId) {
  return [...state.trackDrafts.entries()].filter(([, entry]) => entry.proposalId === proposalId);
}

async function applyTrackDrafts(proposalId, initialProposal) {
  const drafts = trackDraftsFor(proposalId);
  let updated = initialProposal;
  for (const [, entry] of drafts) {
    updated = await api().UpdateTrack(proposalId, entry.update);
    storeProposal(updated);
  }
  for (const [key, entry] of drafts) {
    if (state.trackDrafts.get(key) === entry) state.trackDrafts.delete(key);
  }
  return updated;
}

async function saveDirtyDraft(showToast = false) {
  const id = state.dirtyProposalId;
  if (!id) return state.proposals.find((item) => item.id === state.selectedId) || null;
  const proposal = state.proposals.find((item) => item.id === id);
  if (!proposal || id !== state.selectedId || !$("#metadata-form")) return null;
  const revision = state.dirtyRevision;
  try {
    let updated = await api().UpdateProposal(id, formMetadata(proposal.metadata));
    storeProposal(updated);
    updated = await applyTrackDrafts(id, updated);
    if (state.dirtyRevision === revision) {
      state.dirtyProposalId = null;
      $("#save")?.classList.remove("dirty");
      if ($("#save")) $("#save").textContent = "Änderungen speichern";
      document.querySelectorAll(".track-row.dirty").forEach((row) => row.classList.remove("dirty"));
      if ($("#save-track-changes")) $("#save-track-changes").disabled = trackDraftsFor(id).length === 0;
      renderList();
    }
    invalidatePlan();
    if (showToast) toast("Änderungen gespeichert.");
    return updated;
  } catch (error) {
    toast(`Änderungen konnten nicht gespeichert werden: ${String(error)}`, true);
    return null;
  }
}

async function selectProposal(id) {
  if (id === state.selectedId) return;
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  state.selectedId = id;
  state.dirtyProposalId = null;
  render();
}

async function withSavedDraft(action) {
  let proposal = state.proposals.find((item) => item.id === state.selectedId);
  if (state.dirtyProposalId) {
    proposal = await saveDirtyDraft();
    if (!proposal) return;
  }
  await action(proposal);
}

function logSessionOption(session) {
  const date = new Date(session.modifiedAt);
  const timestamp = Number.isNaN(date.getTime()) ? "Unbekannter Zeitpunkt" : date.toLocaleString("de-DE", { dateStyle: "short", timeStyle: "short" });
  return `${session.current ? "Aktuell · " : ""}${timestamp} · ${formatBytes(session.size)}`;
}

async function showSessionLog(requestedSessionId = state.logSessionId) {
  const overlay = $("#log-overlay");
  const content = $("#log-content");
  const selector = $("#log-session-select");
  overlay.classList.remove("hidden");
  content.innerHTML = `<div class="online-loading"><span></span>Log wird geladen …</div>`;
  selector.disabled = true;
  try {
    let sessions = [];
    try {
      sessions = await api().GetLogSessions() || [];
    } catch (_error) {
      // Das aktuelle In-Memory-Log bleibt nutzbar, auch wenn das Archiv nicht gelesen werden kann.
    }
    const selected = sessions.find((session) => session.sessionId === requestedSessionId)
      || sessions.find((session) => session.current)
      || sessions[0];
    const snapshot = selected && !selected.current
      ? await api().GetLogSession(selected.sessionId)
      : await api().GetSessionLog();
    state.logSessionId = snapshot.sessionId || selected?.sessionId || "";
    if (!sessions.length) sessions = [{ sessionId: state.logSessionId, modifiedAt: new Date().toISOString(), size: 0, current: true }];
    selector.innerHTML = sessions.map((session) => `<option value="${escapeHTML(session.sessionId)}" ${session.sessionId === state.logSessionId ? "selected" : ""}>${escapeHTML(logSessionOption(session))}</option>`).join("");
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
  } finally {
    selector.disabled = false;
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

async function showImportHistory() {
  const overlay = $("#history-overlay");
  const content = $("#history-content");
  overlay.classList.remove("hidden");
  content.innerHTML = `<div class="online-loading"><span></span>Importverlauf wird geladen …</div>`;
  try {
    const history = await api().GetImportHistory();
    content.innerHTML = history?.length
      ? `<div class="history-list">${history.map(importRunCard).join("")}</div>`
      : `<div class="no-candidates">Noch keine ausgeführten Importe vorhanden.</div>`;
    content.querySelectorAll("[data-undo-journal]").forEach((button) => {
      button.addEventListener("click", () => undoHistoryRun(button.dataset.undoJournal, button));
    });
  } catch (error) {
    content.innerHTML = `<div class="online-error"><strong>Importverlauf konnte nicht geladen werden</strong><p>${escapeHTML(String(error))}</p></div>`;
  }
}

function importRunCard(run) {
  const timestamp = new Date(run.createdAt).toLocaleString("de-DE", { dateStyle: "medium", timeStyle: "short" });
  const labels = {
    completed: "Abgeschlossen", failed: "Fehlgeschlagen", undone: "Rückgängig gemacht",
    undo_failed: "Undo fehlgeschlagen", running: "Unterbrochen", undoing: "Undo unterbrochen",
  };
  const status = labels[run.status] || run.status;
  return `
    <article class="history-run ${escapeHTML(run.status)}">
      <div class="history-run-head"><time>${escapeHTML(timestamp)}</time><span>${escapeHTML(status)}</span></div>
      <strong title="${escapeHTML(run.targetRoot)}">${escapeHTML(run.targetRoot || "Unbekanntes Ziel")}</strong>
      <p>${run.completed} von ${run.total} Operationen · ${formatBytes(run.totalBytes)}</p>
      ${run.error ? `<div class="history-error">${escapeHTML(run.error)}</div>` : ""}
      <div class="history-run-foot"><code>${escapeHTML(run.journalId)}</code>${run.canUndo ? `<button type="button" class="ghost danger" data-undo-journal="${escapeHTML(run.journalId)}">Rückgängig machen</button>` : ""}</div>
    </article>`;
}

async function undoHistoryRun(journalId, button) {
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  button.disabled = true;
  button.textContent = "Wird rückgängig gemacht …";
  try {
    const result = await api().UndoExecution(journalId);
    if (result.status === "undone") {
      if (state.lastExecution?.journalId === journalId) state.lastExecution = result;
      invalidatePlan("Ein Import wurde rückgängig gemacht. Bitte vor einer weiteren Ausführung eine neue Vorschau erstellen.");
      if (!await refreshProposalsFromBackend()) {
        const ids = new Set(result.proposalIds || []);
        state.proposals.filter((item) => ids.has(item.id)).forEach((item) => { item.status = "confirmed"; });
        renderList();
      }
      toast("Der Import wurde rückgängig gemacht.");
      await showImportHistory();
    } else {
      toast(result.error || "Undo konnte nicht abgeschlossen werden.", true);
      await showImportHistory();
    }
  } catch (error) {
    toast(String(error), true);
    button.disabled = false;
    button.textContent = "Rückgängig machen";
  }
}

async function chooseDirectory(input, title) {
  try {
    const path = await api().SelectDirectory(title);
    if (path) {
      input.value = path;
      savePreferences();
      invalidatePlan();
    }
  } catch (error) {
    toast(String(error), true);
  }
}

function handleScanProgress(progress = {}) {
  if (!state.scanInProgress) return;
  const ranks = { "": 0, discovering: 1, inspecting: 2, completed: 3 };
  const previous = state.scanProgress;
  const eventIsCurrent = ranks[progress.phase] >= ranks[previous.phase];
  const nextPhase = eventIsCurrent ? progress.phase : previous.phase;
  const discovered = Math.max(previous.discovered, Number(progress.discovered) || 0);
  const inspected = Math.max(previous.inspected, Number(progress.inspected) || 0);
  const counterAdvanced = discovered > previous.discovered || inspected > previous.inspected || nextPhase !== previous.phase;
  const currentPath = eventIsCurrent && counterAdvanced ? String(progress.currentPath || previous.currentPath || "") : previous.currentPath;
  state.scanProgress = { phase: nextPhase, discovered, inspected, currentPath };
  const container = $("#scan-progress");
  const label = $("#scan-progress-text");
  const button = $("#scan");
  if (!container || !label || !button) return;
  container.classList.remove("hidden", "error", "completed");
  const currentName = pathBaseName(currentPath);
  if (nextPhase === "completed") {
    container.classList.add("completed");
    label.textContent = `Scan abgeschlossen · ${inspected || discovered} Audiodatei${(inspected || discovered) === 1 ? "" : "en"} geprüft`;
    button.textContent = "Scan abgeschlossen";
  } else if (nextPhase === "inspecting") {
    label.textContent = `${inspected} von ${Math.max(discovered, inspected)} Audiodateien geprüft${currentName ? ` · ${currentName}` : ""}`;
    button.textContent = `Prüfe ${inspected}/${Math.max(discovered, inspected)}`;
  } else {
    label.textContent = `${discovered} Audiodatei${discovered === 1 ? "" : "en"} gefunden · Ordner wird erfasst …`;
    button.textContent = discovered ? `${discovered} gefunden …` : "Ordner wird erfasst …";
  }
}

async function scan() {
  const source = $("#source").value.trim();
  if (!source) return toast("Bitte zuerst einen Quellordner auswählen.", true);
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  const button = $("#scan");
  const cancel = $("#cancel-scan");
  savePreferences();
  invalidatePlan("Ein neuer Scan wurde gestartet; die bisherige Vorschau ist nicht mehr gültig.", true);
  state.scanInProgress = true;
  state.cancellationRequested = false;
  state.scanProgress = { discovered: 0, inspected: 0, phase: "", currentPath: "" };
  button.disabled = true;
  cancel.disabled = false;
  cancel.textContent = "Scan abbrechen";
  cancel.classList.remove("hidden");
  handleScanProgress({ phase: "discovering", discovered: 0, inspected: 0 });
  try {
    const result = await api().Scan(source);
    const scannedFiles = Number(result.summary?.files) || state.scanProgress.discovered;
    handleScanProgress({ phase: "completed", discovered: scannedFiles, inspected: Math.max(state.scanProgress.inspected, scannedFiles) });
    state.proposals = result.proposals || [];
    state.mergeSelection.clear();
    state.trackDrafts.clear();
    state.selectedId = state.proposals[0]?.id || null;
    state.dirtyProposalId = null;
    state.reviewLimit = 200;
    $("#workspace").classList.remove("hidden");
    $("#plan-section").classList.remove("hidden");
    updateScanSummary();
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
    const progress = $("#scan-progress");
    progress.classList.remove("hidden", "completed");
    progress.classList.add("error");
    $("#scan-progress-text").textContent = state.cancellationRequested ? "Scan wurde abgebrochen." : `Scan fehlgeschlagen · ${String(error)}`;
    toast(state.cancellationRequested ? "Scan wurde abgebrochen." : String(error), !state.cancellationRequested);
  } finally {
    state.scanInProgress = false;
    button.disabled = false;
    button.innerHTML = "Lokal scannen <span>→</span>";
    cancel.classList.add("hidden");
  }
}

function render() {
  renderList();
  renderDetail();
}

function resetReviewWindow() {
  state.reviewLimit = 200;
  renderList();
}

function renderList() {
  const list = $("#book-list");
  const proposals = filteredProposals();
  const rendered = proposals.slice(0, state.reviewLimit);
  updateReviewCounts(proposals, rendered.length);
  if (!state.proposals.length) {
    list.innerHTML = `<div class="empty-list">Keine unterstützten Audiodateien gefunden.</div>`;
    return;
  }
  if (!proposals.length) {
    list.innerHTML = `<div class="empty-list">Keine Vorschläge entsprechen dem aktuellen Filter.</div>`;
    return;
  }
  list.innerHTML = rendered.map((proposal) => `
    <div class="book-item ${proposal.id === state.selectedId ? "active" : ""} ${state.mergeSelection.has(proposal.id) ? "merge-selected" : ""}" data-id="${escapeHTML(proposal.id)}" role="button" tabindex="0">
      <input class="book-ready" type="checkbox" aria-label="Für Import auswählen" data-ready-id="${escapeHTML(proposal.id)}"
        ${proposal.status === "confirmed" || proposal.status === "imported" ? "checked" : ""}
        ${proposal.status === "imported" ? "disabled" : ""} />
      <span class="book-copy">
        <strong>${escapeHTML(displayBookTitle(proposal))}</strong>
        <small>${escapeHTML(proposal.metadata.author)} · ${proposal.files.length} Datei${proposal.files.length === 1 ? "" : "en"}</small>
        <small class="source-path" title="${escapeHTML(proposal.groupPath)}">${escapeHTML(displaySourcePath(proposal))}</small>
      </span>
      <label class="book-merge-control ${proposal.status === "imported" ? "disabled" : ""}" title="${proposal.status === "imported" ? "Importierte Vorschläge können nicht verbunden werden" : "Mit anderen Vorschlägen zu einem Hörbuch verbinden"}">
        <input class="book-merge" type="checkbox" aria-label="Zum Zusammenführen markieren: ${escapeHTML(displayBookTitle(proposal))}" data-merge-id="${escapeHTML(proposal.id)}" ${state.mergeSelection.has(proposal.id) ? "checked" : ""} ${proposal.status === "imported" ? "disabled" : ""} />
        <span aria-hidden="true">Verbinden</span>
      </label>
      <span class="status-pill ${statusClass(proposal.status)}">${escapeHTML(statusLabel(proposal.status))}</span>
    </div>
  `).join("") + (rendered.length < proposals.length
    ? `<button type="button" class="load-more-books" id="load-more-books">Weitere ${Math.min(200, proposals.length - rendered.length)} laden</button>`
    : "");
  list.querySelectorAll(".book-item").forEach((item) => {
    item.addEventListener("click", () => selectProposal(item.dataset.id));
    item.addEventListener("keydown", async (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        await selectProposal(item.dataset.id);
      }
    });
  });
  list.querySelectorAll(".book-ready").forEach((checkbox) => {
    checkbox.addEventListener("click", (event) => event.stopPropagation());
    checkbox.addEventListener("change", () => toggleReady(checkbox.dataset.readyId, checkbox.checked));
  });
  list.querySelectorAll(".book-merge-control").forEach((control) => control.addEventListener("click", (event) => event.stopPropagation()));
  list.querySelectorAll(".book-merge").forEach((checkbox) => {
    checkbox.addEventListener("change", () => {
      if (checkbox.checked) state.mergeSelection.add(checkbox.dataset.mergeId);
      else state.mergeSelection.delete(checkbox.dataset.mergeId);
      checkbox.closest(".book-item")?.classList.toggle("merge-selected", checkbox.checked);
      updateMergeControls();
    });
  });
  $("#load-more-books")?.addEventListener("click", () => {
    state.reviewLimit += 200;
    renderList();
  });
}

function evidence(meta, field) {
  const item = meta.evidence?.[field];
  if (!item?.source) return "Keine lokale Quelle";
  return `${item.source.replaceAll("_", " ")} · ${Math.round(item.confidence * 100)} %`;
}

function trackDraftKey(proposalId, path) {
  return `${proposalId}\u0000${path}`;
}

function trackFileWithDraft(proposal, file) {
  const draft = state.trackDrafts.get(trackDraftKey(proposal.id, file.path));
  return draft ? { ...file, ...draft.update } : file;
}

function renderTrackEditor(proposal) {
  const files = (proposal.files || []).map((file) => trackFileWithDraft(proposal, file));
  const draftProposal = { ...proposal, files };
  return `
    <section class="track-editor" aria-labelledby="track-editor-title">
      <header class="track-editor-header">
        <div><p id="track-editor-title">AUDIODATEIEN</p><small>Zieltitel und Nummern sind optional. 0 bedeutet automatisch bzw. keine Disc-Angabe.</small></div>
        <button type="button" class="ghost" id="save-track-changes" ${trackDraftsFor(proposal.id).length ? "" : "disabled"}>Trackänderungen speichern</button>
      </header>
      <div class="track-editor-rows">
        ${files.map((file, index) => `
          <article class="track-row ${file.excluded ? "excluded" : ""}" data-track-path="${escapeHTML(file.path)}">
            <label class="track-split-control" title="Zum Abtrennen markieren">
              <input class="track-split-select" type="checkbox" aria-label="${escapeHTML(file.name)} zum Abtrennen markieren" />
            </label>
            <label class="track-include-control" title="Nur aktivierte Audiodateien werden übertragen">
              <input class="track-included" type="checkbox" ${file.excluded ? "" : "checked"} />
              <span>${file.excluded ? "Aus" : "Mitnehmen"}</span>
            </label>
            <div class="track-main">
              <div class="track-source"><strong title="${escapeHTML(file.path)}">${escapeHTML(file.name)}</strong><small>${formatBytes(file.size)}</small></div>
              <label><span>Zieltitel</span><input class="track-target-title" maxlength="300" value="${escapeHTML(file.targetTitle || "")}" placeholder="Globale Dateinamen-Regel verwenden" aria-label="Zieltitel für ${escapeHTML(file.name)}" /></label>
              <em class="track-target-preview">→ ${escapeHTML(targetAudioName(file, index, draftProposal))}</em>
            </div>
            <label class="track-number-field"><span>Track</span><input class="track-number" type="number" min="0" max="999999" step="1" value="${Number(file.track) || 0}" /></label>
            <label class="track-number-field"><span>Disc</span><input class="track-disc" type="number" min="0" max="9999" step="1" value="${Number(file.disc) || 0}" /></label>
            <div class="track-order-actions" aria-label="Reihenfolge ändern">
              <button type="button" class="track-move" data-direction="-1" aria-label="${escapeHTML(file.name)} nach oben verschieben" ${index === 0 ? "disabled" : ""}>↑</button>
              <button type="button" class="track-move" data-direction="1" aria-label="${escapeHTML(file.name)} nach unten verschieben" ${index === files.length - 1 ? "disabled" : ""}>↓</button>
            </div>
          </article>
        `).join("")}
      </div>
      <footer class="track-editor-footer">
        <span id="split-track-count" aria-live="polite">Keine Tracks zum Abtrennen markiert</span>
        <button type="button" class="ghost" id="split-tracks" disabled>Markierte Tracks als neues Hörbuch abtrennen</button>
      </footer>
    </section>`;
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
      <span class="status-pill ${statusClass(proposal.status)}">${escapeHTML(statusLabel(proposal.status))}</span>
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
    ${renderTrackEditor(proposal)}
    ${renderCompanions(proposal)}
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
  $("#metadata-form").querySelectorAll("input").forEach((input) => input.addEventListener("input", markProposalDirty));
  $("#save").addEventListener("click", () => saveProposal(proposal, false));
  $("#confirm").addEventListener("click", () => saveProposal(proposal, true));
  $("#detail-ready").addEventListener("change", (event) => {
    if (event.target.checked) saveProposal(proposal, true);
    else withSavedDraft((current) => setStatus(current.id, "review_required"));
  });
  $("#exclude").addEventListener("click", () => withSavedDraft((current) => setStatus(current.id, "excluded")));
  $("#online").addEventListener("click", () => withSavedDraft((current) => showProviderChoice(current)));
  $("#ai").addEventListener("click", () => withSavedDraft((current) => showAIChoice(current)));
  setupTrackEditor(proposal);
  setupCompanionEditor(proposal);
}

function readTrackUpdate(row) {
  const number = (selector, maximum) => {
    const parsed = Number.parseInt(row.querySelector(selector)?.value || "0", 10);
    return Math.min(maximum, Math.max(0, Number.isFinite(parsed) ? parsed : 0));
  };
  return {
    path: row.dataset.trackPath,
    targetTitle: String(row.querySelector(".track-target-title")?.value || "").trim(),
    track: number(".track-number", 999999),
    disc: number(".track-disc", 9999),
    excluded: !row.querySelector(".track-included")?.checked,
  };
}

function refreshTrackEditorPreviews(proposal) {
  const rows = [...document.querySelectorAll(".track-row")];
  const updates = new Map(rows.map((row) => [row.dataset.trackPath, readTrackUpdate(row)]));
  const files = (proposal.files || []).map((file) => ({ ...file, ...(updates.get(file.path) || {}) }));
  const draftProposal = { ...proposal, files };
  rows.forEach((row) => {
    const file = files.find((item) => item.path === row.dataset.trackPath);
    if (!file) return;
    const index = files.findIndex((item) => item.path === file.path);
    row.classList.toggle("excluded", file.excluded);
    const label = row.querySelector(".track-include-control span");
    if (label) label.textContent = file.excluded ? "Aus" : "Mitnehmen";
    const preview = row.querySelector(".track-target-preview");
    if (preview) preview.textContent = `→ ${targetAudioName(file, index, draftProposal)}`;
  });
}

function updateSplitTrackControls() {
  const checkboxes = [...document.querySelectorAll(".track-split-select")];
  const count = checkboxes.filter((input) => input.checked).length;
  const label = $("#split-track-count");
  const button = $("#split-tracks");
  if (label) label.textContent = count ? `${count} Track${count === 1 ? "" : "s"} zum Abtrennen markiert` : "Keine Tracks zum Abtrennen markiert";
  if (button) button.disabled = state.trackMutationInProgress || count === 0 || count === checkboxes.length;
}

function captureTrackDraft(proposal, row) {
  const update = readTrackUpdate(row);
  state.trackDrafts.set(trackDraftKey(proposal.id, update.path), { proposalId: proposal.id, update });
  row.classList.add("dirty");
  const button = $("#save-track-changes");
  if (button) button.disabled = false;
  refreshTrackEditorPreviews(proposal);
  markProposalDirty();
}

function setTrackEditorBusy(busy) {
  state.trackMutationInProgress = busy;
  document.querySelectorAll(".track-editor button, .track-editor input").forEach((control) => { control.disabled = busy; });
  if (!busy) {
    document.querySelectorAll(".track-move[data-direction='-1']").forEach((button, index) => { button.disabled = index === 0; });
    const down = [...document.querySelectorAll(".track-move[data-direction='1']")];
    down.forEach((button, index) => { button.disabled = index === down.length - 1; });
    if ($("#save-track-changes")) $("#save-track-changes").disabled = trackDraftsFor(state.selectedId).length === 0;
    updateSplitTrackControls();
  }
}

function setupTrackEditor(proposal) {
  document.querySelectorAll(".track-row").forEach((row) => {
    row.querySelectorAll(".track-target-title, .track-number, .track-disc").forEach((input) => {
      input.addEventListener("input", () => captureTrackDraft(proposal, row));
    });
    row.querySelector(".track-included")?.addEventListener("change", () => captureTrackDraft(proposal, row));
    row.querySelector(".track-split-select")?.addEventListener("change", updateSplitTrackControls);
    row.querySelectorAll(".track-move").forEach((button) => {
      button.addEventListener("click", () => moveTrack(proposal.id, row.dataset.trackPath, Number(button.dataset.direction)));
    });
  });
  $("#save-track-changes")?.addEventListener("click", async () => {
    if (!await saveDirtyDraft(true)) return;
    renderDetail();
  });
  $("#split-tracks")?.addEventListener("click", () => splitSelectedTracks(proposal.id));
  updateSplitTrackControls();
}

async function moveTrack(proposalId, path, direction) {
  if (state.trackMutationInProgress) return;
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  const proposal = state.proposals.find((item) => item.id === proposalId);
  const paths = (proposal?.files || []).map((file) => file.path);
  const index = paths.indexOf(path);
  const target = index + direction;
  if (index < 0 || target < 0 || target >= paths.length) return;
  [paths[index], paths[target]] = [paths[target], paths[index]];
  setTrackEditorBusy(true);
  try {
    const updated = await api().ReorderTracks(proposalId, paths);
    replaceProposal(updated);
    toast("Trackreihenfolge wurde gespeichert.");
  } catch (error) {
    toast(`Reihenfolge konnte nicht geändert werden: ${String(error)}`, true);
  } finally {
    setTrackEditorBusy(false);
  }
}

async function splitSelectedTracks(proposalId) {
  if (state.trackMutationInProgress) return;
  const selectedPaths = [...document.querySelectorAll(".track-row")]
    .filter((row) => row.querySelector(".track-split-select")?.checked)
    .map((row) => row.dataset.trackPath);
  const total = state.proposals.find((item) => item.id === proposalId)?.files?.length || 0;
  if (!selectedPaths.length || selectedPaths.length >= total) return toast("Zum Aufteilen müssen auf beiden Seiten Tracks verbleiben.", true);
  if (!window.confirm(`${selectedPaths.length} von ${total} Tracks als neuen Hörbuch-Vorschlag abtrennen?`)) return;
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  setTrackEditorBusy(true);
  invalidatePlan("Tracks werden abgetrennt; die bisherige Vorschau ist nicht mehr gültig.");
  try {
    const result = await api().SplitProposal(proposalId, selectedPaths);
    const original = result?.[0];
    const created = result?.[1];
    if (!original || !created) throw new Error("Die App hat keine vollständigen Split-Ergebnisse geliefert");
    state.selectedId = created.id;
    state.dirtyProposalId = null;
    if (!await refreshProposalsFromBackend()) {
      const index = state.proposals.findIndex((item) => item.id === proposalId);
      if (index >= 0) state.proposals.splice(index, 1, original, created);
      updateScanSummary();
      render();
    }
    toast("Tracks wurden als neuer Vorschlag abgetrennt. Beide Vorschläge bitte prüfen.");
  } catch (error) {
    toast(`Aufteilen fehlgeschlagen: ${String(error)}`, true);
  } finally {
    setTrackEditorBusy(false);
  }
}

async function toggleReady(id, checked) {
  if (state.dirtyProposalId && !await saveDirtyDraft()) {
    render();
    return;
  }
  try {
    replaceProposal(await api().SetProposalStatus(id, checked ? "confirmed" : "review_required"));
    toast(checked ? "Hörbuch für den Import ausgewählt." : "Hörbuch bleibt im Quellordner.");
  } catch (error) {
    toast(String(error), true);
    render();
  }
}

async function bulkSetVisible(mode) {
  if (state.bulkInProgress) return;
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  const visible = filteredProposals();
  let targets = visible.filter((proposal) => proposal.status !== "imported");
  let status = "confirmed";
  if (mode === "safe") targets = targets.filter(proposalIsSafe);
  if (mode === "clear") {
    status = "review_required";
    targets = targets.filter((proposal) => proposal.status === "confirmed");
  } else {
    targets = targets.filter((proposal) => proposal.status !== "confirmed" && proposalIsValid(proposal));
  }
  if (!targets.length) {
    toast(mode === "safe" ? "Keine sicheren sichtbaren Vorschläge gefunden." : "Keine passenden sichtbaren Vorschläge zu ändern.");
    return;
  }
  state.bulkInProgress = true;
  invalidatePlan();
  updateReviewCounts(visible);
  try {
    const updated = await api().BulkSetProposalStatus(targets.map((proposal) => proposal.id), status);
    (updated || []).forEach(storeProposal);
    toast(`${updated?.length || 0} sichtbare Vorschläge wurden ${status === "confirmed" ? "ausgewählt" : "abgewählt"}.`);
  } catch (error) {
    toast(`Sammeländerung fehlgeschlagen; es wurde nichts geändert: ${String(error)}`, true);
  } finally {
    state.bulkInProgress = false;
    render();
  }
}

function clearMergeSelection() {
  state.mergeSelection.clear();
  renderList();
}

async function mergeSelectedProposals() {
  if (state.bulkInProgress) return;
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  pruneMergeSelection();
  const ids = [...state.mergeSelection];
  if (ids.length < 2) return toast("Bitte mindestens zwei Vorschläge zum Zusammenführen markieren.", true);
  const selected = state.proposals.filter((proposal) => state.mergeSelection.has(proposal.id));
  const description = selected.slice(0, 4).map((proposal) => displayBookTitle(proposal)).join("\n• ");
  const more = selected.length > 4 ? `\n… und ${selected.length - 4} weitere` : "";
  if (!window.confirm(`Diese ${selected.length} Vorschläge zu einem Hörbuch verbinden?\n\n• ${description}${more}\n\nDie Metadaten des ersten Vorschlags werden übernommen.`)) return;
  const firstIndex = Math.max(0, state.proposals.findIndex((proposal) => state.mergeSelection.has(proposal.id)));
  state.bulkInProgress = true;
  invalidatePlan("Vorschläge werden zusammengeführt; die bisherige Vorschau ist nicht mehr gültig.");
  updateReviewCounts();
  try {
    const merged = await api().MergeProposals(ids);
    state.mergeSelection.clear();
    state.selectedId = merged.id;
    state.dirtyProposalId = null;
    const refreshed = await refreshProposalsFromBackend();
    if (!refreshed) {
      const removed = new Set(ids);
      state.proposals = state.proposals.filter((proposal) => !removed.has(proposal.id));
      state.proposals.splice(Math.min(firstIndex, state.proposals.length), 0, merged);
      updateScanSummary();
      render();
    }
    toast(`${ids.length} Vorschläge wurden zusammengeführt. Reihenfolge und Metadaten bitte prüfen.`);
  } catch (error) {
    toast(`Zusammenführen fehlgeschlagen: ${String(error)}`, true);
  } finally {
    state.bulkInProgress = false;
    updateReviewCounts();
  }
}

async function selectNextReview() {
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  const visible = filteredProposals();
  let candidates = visible.filter((proposal) => proposal.status === "review_required");
  if (!candidates.length) {
    $("#review-search").value = "";
    $("#review-status").value = "review_required";
    state.reviewLimit = 200;
    candidates = filteredProposals().filter((proposal) => proposal.status === "review_required");
  }
  if (!candidates.length) return toast("Alle Vorschläge wurden bereits bearbeitet.");
  const current = candidates.findIndex((proposal) => proposal.id === state.selectedId);
  const next = candidates[(current + 1) % candidates.length];
  const nextVisibleIndex = filteredProposals().findIndex((proposal) => proposal.id === next.id);
  if (nextVisibleIndex >= 0) state.reviewLimit = Math.max(state.reviewLimit, nextVisibleIndex + 1);
  state.selectedId = next.id;
  state.dirtyProposalId = null;
  render();
  [...document.querySelectorAll(".book-item")].find((item) => item.dataset.id === next.id)?.scrollIntoView({ block: "nearest" });
}

function renderCompanions(proposal) {
  const companions = proposal.companions || [];
  if (!companions.length) return "";
  const targets = state.proposals.filter((item) => item.id !== proposal.id && item.status !== "imported" && item.sourceRoot === proposal.sourceRoot);
  return `
    <div class="companion-box">
      <header><div><p>WEITERE DATEIEN</p><small>Behandlung pro Datei festlegen oder Dateien einem anderen Vorschlag zuordnen.</small></div></header>
      ${companions.map((file) => `<div class="companion-row" data-companion-path="${escapeHTML(file.path)}">
        <input class="companion-move-select" type="checkbox" aria-label="${escapeHTML(file.name)} zum Verschieben markieren" />
        <strong title="${escapeHTML(file.path)}">${escapeHTML(file.name)}</strong>
        <small>${formatBytes(file.size)}</small>
        <select class="companion-kind-select" aria-label="Behandlung für ${escapeHTML(file.name)}">
          <option value="ebook" ${file.kind === "ebook" ? "selected" : ""}>Als E-Book einsortieren</option>
          <option value="discard" ${file.kind === "discard" ? "selected" : ""}>Nach Import entfernen</option>
          <option value="unknown" ${file.kind !== "ebook" && file.kind !== "discard" ? "selected" : ""}>Im Quellordner belassen</option>
        </select>
      </div>`).join("")}
      <footer class="companion-move-controls">
        <span id="companion-selection-count">Keine Datei markiert</span>
        <select id="companion-target" aria-label="Zielvorschlag" ${targets.length ? "" : "disabled"}>
          <option value="">Zielvorschlag wählen …</option>
          ${targets.map((item) => `<option value="${escapeHTML(item.id)}">${escapeHTML(displayBookTitle(item))} · ${escapeHTML(item.metadata.author)}</option>`).join("")}
        </select>
        <button type="button" class="ghost" id="move-companions" disabled>Zuordnen</button>
      </footer>
    </div>
  `;
}

function updateCompanionMoveControls() {
  const selected = [...document.querySelectorAll(".companion-move-select")].filter((input) => input.checked).length;
  const target = $("#companion-target")?.value || "";
  const count = $("#companion-selection-count");
  if (count) count.textContent = selected ? `${selected} Datei${selected === 1 ? "" : "en"} markiert` : "Keine Datei markiert";
  if ($("#move-companions")) $("#move-companions").disabled = !selected || !target;
}

function setupCompanionEditor(proposal) {
  document.querySelectorAll(".companion-row").forEach((row) => {
    row.querySelector(".companion-move-select")?.addEventListener("change", updateCompanionMoveControls);
    row.querySelector(".companion-kind-select")?.addEventListener("change", async (event) => {
      if (state.dirtyProposalId && !await saveDirtyDraft()) return renderDetail();
      event.target.disabled = true;
      invalidatePlan();
      try {
        const updated = await api().UpdateCompanion(proposal.id, row.dataset.companionPath, event.target.value);
        replaceProposal(updated);
        updateScanSummary();
        toast("Behandlung der Begleitdatei wurde gespeichert.");
      } catch (error) {
        toast(`Begleitdatei konnte nicht geändert werden: ${String(error)}`, true);
        renderDetail();
      }
    });
  });
  $("#companion-target")?.addEventListener("change", updateCompanionMoveControls);
  $("#move-companions")?.addEventListener("click", async () => {
    const targetId = $("#companion-target")?.value || "";
    const paths = [...document.querySelectorAll(".companion-row")]
      .filter((row) => row.querySelector(".companion-move-select")?.checked)
      .map((row) => row.dataset.companionPath);
    if (!targetId || !paths.length) return;
    if (state.dirtyProposalId && !await saveDirtyDraft()) return;
    const button = $("#move-companions");
    button.disabled = true;
    invalidatePlan("Begleitdateien werden neu zugeordnet; die bisherige Vorschau ist nicht mehr gültig.");
    try {
      const updated = await api().MoveCompanions(proposal.id, targetId, paths);
      (updated || []).forEach(storeProposal);
      updateScanSummary();
      render();
      toast(`${paths.length} Begleitdatei${paths.length === 1 ? " wurde" : "en wurden"} neu zugeordnet.`);
    } catch (error) {
      toast(`Zuordnung fehlgeschlagen: ${String(error)}`, true);
      renderDetail();
    }
  });
  updateCompanionMoveControls();
}

function showProviderChoice(proposal, suggestedQuery = {}) {
  const panel = $("#online-panel");
  const metadata = proposal.metadata || {};
  panel.classList.remove("hidden");
  panel.innerHTML = `
    <div class="online-heading">
      <div><p class="detail-kicker">BEWUSSTE ONLINE-ANFRAGE</p><strong>Metadatenanbieter wählen</strong></div>
      <button class="panel-close" aria-label="Schließen">×</button>
    </div>
    <p class="online-copy">Prüfe die Suchangaben. Titel, Autor und IDs werden vor der Suche zugleich als bearbeiteter Vorschlag gespeichert; die Region betrifft nur Audible.</p>
    <form class="online-query-form" id="online-query-form">
      <label><span>Titel</span><input name="title" required value="${escapeHTML(suggestedQuery.title || metadata.title || "")}" /></label>
      <label><span>Autor</span><input name="author" required value="${escapeHTML(suggestedQuery.author || metadata.author || "")}" /></label>
      <label><span>ASIN</span><input name="asin" value="${escapeHTML(metadata.asin || "")}" /></label>
      <label><span>ISBN</span><input name="isbn" value="${escapeHTML(metadata.isbn || "")}" /></label>
      <label><span>Audible-Region</span><select name="region">
        <option value="de" selected>Deutschland</option><option value="us">USA</option><option value="uk">Großbritannien</option>
        <option value="fr">Frankreich</option><option value="it">Italien</option><option value="es">Spanien</option>
        <option value="au">Australien</option><option value="ca">Kanada</option><option value="jp">Japan</option><option value="in">Indien</option>
      </select></label>
    </form>
    <div class="provider-actions">
      <button type="button" class="provider-button audible-provider"><b>A</b><span><strong>Audible durchsuchen</strong><small>Hörbuchausgabe, Sprecher, Laufzeit und Serie</small></span></button>
      <button type="button" class="provider-button google-provider"><b>G</b><span><strong>Google Books durchsuchen</strong><small>Buchtitel, Autor, ISBN und Verlag</small></span></button>
    </div>
  `;
  panel.querySelector(".panel-close").addEventListener("click", render);
  panel.querySelector("#online-query-form").addEventListener("submit", (event) => event.preventDefault());
  panel.querySelector(".audible-provider").addEventListener("click", () => searchOnlineFromForm(proposal, "audible"));
  panel.querySelector(".google-provider").addEventListener("click", () => searchOnlineFromForm(proposal, "google_books"));
  panel.scrollIntoView({ behavior: "smooth", block: "nearest" });
}

async function searchOnlineFromForm(proposal, provider) {
  const form = $("#online-query-form");
  if (!form?.reportValidity()) return;
  const data = new FormData(form);
  const metadata = {
    ...proposal.metadata,
    title: String(data.get("title") || "").trim(),
    author: String(data.get("author") || "").trim(),
    asin: String(data.get("asin") || "").trim(),
    isbn: String(data.get("isbn") || "").trim(),
  };
  try {
    const updated = await api().UpdateProposal(proposal.id, metadata);
    storeProposal(updated);
    state.dirtyProposalId = null;
    invalidatePlan();
    await searchOnline(updated, provider, {
      title: metadata.title, author: metadata.author, narrator: metadata.narrator || "",
      asin: metadata.asin, isbn: metadata.isbn, language: metadata.language || "",
      region: String(data.get("region") || "de"),
    });
  } catch (error) {
    toast(String(error), true);
  }
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
    ["Audible-Suchbegriff", suggestion.suggestedSearchTitle], ["Audible-Suchautor", suggestion.suggestedSearchAuthor],
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
    const suggestion = state.aiSuggestions[proposalId];
    const updated = await api().ApplyAISuggestion(proposalId);
    replaceProposal(updated);
    if (searchAudible) {
      showProviderChoice(updated, {
        title: suggestion?.suggestedSearchTitle || updated.metadata.title,
        author: suggestion?.suggestedSearchAuthor || updated.metadata.author,
      });
      toast("AI-Ergebnis übernommen. Bitte die Audible-Suchangaben prüfen.");
    } else {
      toast("AI-Ergebnis als prüfbarer Vorschlag übernommen.");
    }
  } catch (error) {
    toast(String(error), true);
  }
}

async function searchOnline(proposal, provider, query = {}) {
  const panel = $("#online-panel");
  panel.innerHTML = `<div class="online-loading"><span></span>${provider === "audible" ? "Audible" : "Google Books"} wird durchsucht …</div>`;
  try {
    const region = String(query.region || "de");
    const candidates = await api().SearchOnlineWithQuery(proposal.id, provider, {
      title: String(query.title || proposal.metadata.title || ""),
      author: String(query.author || proposal.metadata.author || ""),
      narrator: String(query.narrator || proposal.metadata.narrator || ""),
      asin: String(query.asin || proposal.metadata.asin || ""),
      isbn: String(query.isbn || proposal.metadata.isbn || ""),
      language: String(query.language || proposal.metadata.language || ""),
      region,
    });
    renderCandidates(proposal, candidates || [], provider, region);
  } catch (error) {
    panel.innerHTML = `<div class="online-error"><strong>Suche fehlgeschlagen</strong><p>${escapeHTML(String(error))}</p><button class="ghost retry-online">Anbieterauswahl</button></div>`;
    panel.querySelector(".retry-online").addEventListener("click", () => showProviderChoice(proposal));
  }
}

function renderCandidates(proposal, candidates, provider, region = "de") {
  const panel = $("#online-panel");
  panel.innerHTML = `
    <div class="online-heading">
      <div><p class="detail-kicker">${provider === "audible" ? `AUDIBLE · ${escapeHTML(region.toUpperCase())}` : "GOOGLE BOOKS"}</p><strong>${candidates.length} Treffer</strong></div>
      <button class="panel-close" aria-label="Schließen">×</button>
    </div>
    ${candidates.length ? `<div class="candidate-list">${candidates.map(candidateCard).join("")}</div>` : `<div class="no-candidates">Keine passenden Treffer gefunden.</div>`}
    <button class="ghost change-provider">Anderen Anbieter wählen</button>
  `;
  panel.querySelector(".panel-close").addEventListener("click", render);
  panel.querySelector(".change-provider").addEventListener("click", () => showProviderChoice(proposal));
  panel.querySelectorAll(".apply-candidate").forEach((button) => {
    button.addEventListener("click", () => applyCandidate(proposal.id, button.dataset.candidateId));
  });
}

function candidateCard(candidate) {
  const coverURL = safeHTTPSURL(candidate.coverUrl);
  const details = [
    candidate.series ? `${candidate.series}${candidate.seriesSequence ? ` · Band ${candidate.seriesSequence}` : ""}` : "",
    candidate.narrator ? `Sprecher: ${candidate.narrator}` : "",
    candidate.durationMinutes ? `Laufzeit: ${Math.floor(candidate.durationMinutes / 60)} Std. ${candidate.durationMinutes % 60} Min.` : "",
    candidate.isbn ? `ISBN: ${candidate.isbn}` : candidate.asin ? `ASIN: ${candidate.asin}` : "",
  ].filter(Boolean);
  return `
    <article class="candidate-card">
      <div class="candidate-cover">${coverURL ? `<img src="${escapeHTML(coverURL)}" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" />` : `<span>${candidate.provider === "audible" ? "A" : "G"}</span>`}</div>
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
    clearTimeout(markProposalDirty.timer);
    let updated = await api().UpdateProposal(proposal.id, formMetadata(proposal.metadata));
    storeProposal(updated);
    updated = await applyTrackDrafts(proposal.id, updated);
    if (confirm) updated = await api().SetProposalStatus(proposal.id, "confirmed");
    state.dirtyProposalId = null;
    state.dirtyRevision += 1;
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

function replaceProposal(updated, { invalidate = true, renderNow = true } = {}) {
  storeProposal(updated);
  if (state.dirtyProposalId === updated.id) state.dirtyProposalId = null;
  if (invalidate) invalidatePlan();
  if (renderNow) render();
}

async function buildPlan() {
  const target = $("#target").value.trim();
  if (!target) return toast("Bitte einen Zielordner auswählen.", true);
  if (state.dirtyProposalId && !await saveDirtyDraft()) return;
  try {
    savePreferences();
    const options = planOptions();
    const plan = await api().BuildPlan(target, options);
    state.lastPlan = plan;
    state.lastPlanOptions = options;
    state.lastExecution = null;
    if (plan.executable && !plan.planId) {
      invalidatePlan("Die Vorschau besitzt keine gültige Plan-ID und kann aus Sicherheitsgründen nicht ausgeführt werden.");
      return toast("Die Vorschau konnte nicht sicher gebunden werden. Bitte die aktuelle App-Version verwenden.", true);
    }
    const output = $("#plan-output");
    output.innerHTML = `
      <div class="plan-summary ${plan.executable ? "ready" : "blocked"}">
        <strong>${plan.executable ? "Plan ist konfliktfrei" : "Plan benötigt Aufmerksamkeit"}</strong>
        <span>${plan.operations.length} Operationen · ${formatBytes(plan.totalBytes)}</span>
      </div>
      ${plan.warnings?.length ? `<div class="warning-list">${plan.warnings.map(escapeHTML).join("<br>")}</div>` : ""}
      ${renderOperationGroups(plan.operations)}
      ${plan.executable ? `
        <div class="execution-box">
          <label><input type="checkbox" id="execution-consent" /> Ich habe Quelle und Ziel geprüft und möchte die angezeigten Dateien verschieben.</label>
          <div class="execution-actions"><button type="button" class="ghost danger hidden" id="cancel-execution">Abbrechen</button><button type="button" class="primary compact" id="execute-plan" disabled>Dateien verschieben</button></div>
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
      $("#cancel-execution").addEventListener("click", () => cancelActiveJob("execution"));
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
    existingFilePolicy: $("#existing-file-policy")?.value || "error",
  };
}

function operationRow(operation) {
  const label = operation.action === "remove"
    ? "Entfernen (Undo-fähig)"
    : operation.action === "deduplicate"
      ? `${operation.target} (identisch; Quelle in Quarantäne)`
      : operation.target;
  const category = { audio: "Audio", ebook: "E-Book", sidecar: "Bereinigung" }[operation.category] || "Datei";
  return `<div class="operation-row"><i class="operation-category ${escapeHTML(operation.category)}">${category}</i><span title="${escapeHTML(operation.source)}">${escapeHTML(operation.source)}</span><b>→</b><strong title="${escapeHTML(label)}">${escapeHTML(label)}</strong></div>`;
}

function renderOperationGroups(operations = []) {
  const groups = new Map();
  for (const operation of operations) {
    const key = operation.proposalId || "other";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(operation);
  }
  const content = [...groups.entries()].map(([proposalId, items], index) => {
    const proposal = state.proposals.find((entry) => entry.id === proposalId);
    const title = proposal ? displayBookTitle(proposal) : "Weitere Operationen";
    const bytes = items.reduce((sum, item) => sum + Number(item.size || 0), 0);
    const categories = [...new Set(items.map((item) => ({ audio: "Audio", ebook: "E-Book", sidecar: "Bereinigung" }[item.category] || "Datei")))].join(" · ");
    return `<details class="operation-group" ${groups.size <= 3 || index === 0 ? "open" : ""}>
      <summary><span><strong>${escapeHTML(title)}</strong><small>${items.length} Operation${items.length === 1 ? "" : "en"} · ${escapeHTML(categories)}</small></span><b>${formatBytes(bytes)}</b></summary>
      <div class="operation-group-rows">${items.map(operationRow).join("")}</div>
    </details>`;
  }).join("");
  return `<div class="operation-list">${content || `<div class="empty-list">Keine Dateioperationen.</div>`}</div>`;
}

async function executePlan() {
  const count = state.lastPlan?.operations.length || 0;
  const planId = state.lastPlan?.planId || "";
  if (!count || !planId) return toast("Bitte zuerst eine aktuelle, ausführbare Vorschau erstellen.", true);
  const button = $("#execute-plan");
  const cancel = $("#cancel-execution");
  const status = $("#execution-status");
  if (!button || button.disabled) return;
  button.disabled = true;
  button.textContent = "Dateien werden verschoben …";
  status.textContent = `Übertragung gestartet: 0 von ${count} Operationen abgeschlossen. Bitte die App geöffnet lassen.`;
  status.className = "execution-status running";
  state.executionInProgress = true;
  state.cancellationRequested = false;
  cancel?.classList.remove("hidden");
  if (cancel) {
    cancel.disabled = false;
    cancel.textContent = "Abbrechen";
  }
  showExecutionProgress(0);
  await new Promise((resolve) => requestAnimationFrame(() => resolve()));
  try {
    const result = await api().ExecutePlan(planId);
    state.lastExecution = result;
    renderExecutionResult(result);
    const refreshed = await refreshProposalsFromBackend();
    if (result.status === "completed") {
      if (!refreshed) {
        const completedIDs = new Set((result.proposalResults || []).filter((item) => item.status === "completed").map((item) => item.proposalId));
        state.proposals.filter((item) => completedIDs.has(item.id)).forEach((item) => { item.status = "imported"; });
        renderList();
      }
      toast(result.warnings?.length
        ? "Dateien wurden übertragen; einige Quellen konnten nicht gelöscht werden."
        : "Dateien wurden erfolgreich verschoben.");
    } else {
      if (!refreshed && result.proposalResults?.length) {
        for (const proposalResult of result.proposalResults) {
          const proposal = state.proposals.find((item) => item.id === proposalResult.proposalId);
          if (!proposal) continue;
          if (proposalResult.status === "completed") proposal.status = "imported";
          else if (proposalResult.status === "failed") proposal.status = "error";
          else proposal.status = "confirmed";
        }
        renderList();
      }
      toast(result.error || "Das Verschieben wurde nicht vollständig abgeschlossen.", true);
    }
  } catch (error) {
    toast(state.cancellationRequested ? "Abbruch wurde angefordert; der aktuelle sichere Dateischritt wurde beendet." : String(error), !state.cancellationRequested);
    button.disabled = true;
    button.textContent = "Vorschau neu erstellen";
    status.textContent = state.cancellationRequested ? "Verschieben wurde sicher abgebrochen." : `Verschieben nicht gestartet oder unterbrochen: ${String(error)}`;
    status.className = "execution-status error";
    if (isSourceError(error)) addRescanButton(status);
  } finally {
    state.executionInProgress = false;
    state.lastPlan = null;
    state.lastPlanOptions = null;
    cancel?.classList.add("hidden");
  }
}

async function cancelActiveJob(kind) {
  const button = kind === "scan" ? $("#cancel-scan") : $("#cancel-execution");
  state.cancellationRequested = true;
  if (button) {
    button.disabled = true;
    button.textContent = "Abbruch angefordert …";
  }
  try {
    await api().CancelActiveJob();
    toast("Abbruch angefordert. Bereits sicher abgeschlossene Schritte bleiben journalisiert.");
  } catch (error) {
    state.cancellationRequested = false;
    if (button) {
      button.disabled = false;
      button.textContent = kind === "scan" ? "Scan abbrechen" : "Abbrechen";
    }
    toast(`Abbruch konnte nicht angefordert werden: ${String(error)}`, true);
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
  const retainedSources = successful && result.warnings?.some((warning) => String(warning).includes("Löschrechte"));
  const canUndo = !undone && result.completed > 0 && result.journalId;
  const needsRescan = isSourceError(result.error);
  output.innerHTML = `
    <div class="plan-summary ${successful || undone ? "ready" : "blocked"}">
      <strong>${successful ? retainedSources ? "Dateien übertragen – Quellen teilweise behalten" : "Dateien verschoben" : undone ? "Verschieben rückgängig gemacht" : "Verschieben unterbrochen"}</strong>
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
    state.lastPlan = null;
    state.lastPlanOptions = null;
    renderExecutionResult(undone);
    if (undone.status === "undone") {
      if (!await refreshProposalsFromBackend()) {
        const ids = new Set(undone.proposalIds || []);
        state.proposals.filter((item) => ids.has(item.id)).forEach((item) => { item.status = "confirmed"; });
        renderList();
      }
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

async function restorePersistedReview() {
  try {
    const proposals = await api().GetProposals();
    if (!proposals?.length) return;
    state.proposals = proposals;
    state.reviewLimit = 200;
    state.selectedId = proposals.find((item) => item.status === "review_required")?.id || proposals[0].id;
    if (proposals[0].sourceRoot) {
      $("#source").value = proposals[0].sourceRoot;
      savePreferences();
    }
    $("#workspace").classList.remove("hidden");
    $("#plan-section").classList.remove("hidden");
    updateScanSummary();
    const notice = $("#notice");
    notice.textContent = "Die zuletzt gespeicherte Prüfsitzung wurde wiederhergestellt. Quelldateien werden vor einer Ausführung erneut geprüft.";
    notice.classList.remove("hidden");
    render();
  } catch (error) {
    toast(`Gespeicherte Prüfsitzung konnte nicht geladen werden: ${String(error)}`, true);
  }
}

restorePreferences();

$("#choose-source").addEventListener("click", () => chooseDirectory($("#source"), "Quellordner auswählen"));
$("#choose-target").addEventListener("click", () => chooseDirectory($("#target"), "Zielordner auswählen"));
$("#scan").addEventListener("click", scan);
$("#cancel-scan").addEventListener("click", () => cancelActiveJob("scan"));
$("#build-plan").addEventListener("click", buildPlan);
$("#review-search").addEventListener("input", resetReviewWindow);
$("#review-status").addEventListener("change", resetReviewWindow);
$("#review-sort").addEventListener("change", resetReviewWindow);
$("#select-safe-visible").addEventListener("click", () => bulkSetVisible("safe"));
$("#select-visible").addEventListener("click", () => bulkSetVisible("all"));
$("#deselect-visible").addEventListener("click", () => bulkSetVisible("clear"));
$("#clear-merge-selection").addEventListener("click", clearMergeSelection);
$("#merge-selected").addEventListener("click", mergeSelectedProposals);
$("#next-review").addEventListener("click", selectNextReview);
$("#open-history").addEventListener("click", showImportHistory);
$("#open-log").addEventListener("click", () => showSessionLog());
$("#open-ai-profiles").addEventListener("click", showAIProfiles);
$("#close-ai-profiles").addEventListener("click", () => $("#ai-profile-overlay").classList.add("hidden"));

window.runtime?.EventsOn?.("execution:progress", handleExecutionProgress);
window.runtime?.EventsOn?.("scan:progress", handleScanProgress);
$("#ai-profile-form").addEventListener("submit", saveAIProfile);
$("#test-ai-profile").addEventListener("click", testAIProfile);
$("#delete-ai-profile").addEventListener("click", deleteAIProfile);
$("#ai-profile-provider").addEventListener("change", (event) => {
  document.querySelector('#ai-profile-form [name="baseUrl"]').value = aiProviderDefaults[event.target.value] || "";
});
$("#refresh-log").addEventListener("click", () => showSessionLog());
$("#log-session-select").addEventListener("change", (event) => showSessionLog(event.target.value));
$("#close-log").addEventListener("click", () => $("#log-overlay").classList.add("hidden"));
$("#refresh-history").addEventListener("click", showImportHistory);
$("#close-history").addEventListener("click", () => $("#history-overlay").classList.add("hidden"));
[$("#book-number-width"), $("#track-number-width"), $("#audio-file-naming"), $("#move-ebooks"), $("#cleanup-sidecars")].forEach((input) => {
  input.addEventListener("change", async () => {
    savePreferences();
    if (state.dirtyProposalId && !await saveDirtyDraft()) return;
    invalidatePlan();
    renderDetail();
  });
});
[$("#source"), $("#target")].forEach((input) => input.addEventListener("input", () => {
  savePreferences();
  invalidatePlan();
}));

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
$("#history-overlay").addEventListener("click", (event) => {
  if (event.target === $("#history-overlay")) $("#history-overlay").classList.add("hidden");
});
$("#ai-profile-overlay").addEventListener("click", (event) => {
  if (event.target === $("#ai-profile-overlay")) $("#ai-profile-overlay").classList.add("hidden");
});
document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "s" && state.dirtyProposalId) {
    event.preventDefault();
    saveDirtyDraft(true);
    return;
  }
  if (event.key === "Escape") {
    $("#history-overlay").classList.add("hidden");
    $("#log-overlay").classList.add("hidden");
    $("#ai-profile-overlay").classList.add("hidden");
  }
});
window.addEventListener("beforeunload", (event) => {
  if (!state.dirtyProposalId && !state.executionInProgress && !state.scanInProgress) return;
  event.preventDefault();
  event.returnValue = "";
});

restorePersistedReview();
