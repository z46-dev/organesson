import { useEffect, useState } from "react";
import { RotateCcw, Trash2 } from "lucide-react";
import type { ApiRequest } from "./api";

type Snapshot = { id: number; name: string; description: string; reserved_gib: number; state: string; created_at: string };
type Props = { resourceID: number; request: ApiRequest; onError: (message: string) => void };

// Provides the managed snapshot lifecycle for one VM.
export function VMSnapshotPanel({ resourceID, request, onError }: Props) {
    const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
    const [description, setDescription] = useState("");
    const [busy, setBusy] = useState(false);

    async function loadSnapshots() {
        try {
            const result = await request<{ snapshots: Snapshot[] }>(`/virtual-machines/${resourceID}/snapshots`);
            setSnapshots(result.snapshots ?? []);
        } catch (error) {
            onError((error as Error).message);
        }
    }

    useEffect(() => {
        loadSnapshots();
    }, [resourceID]);

    async function createSnapshot(event: React.FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        try {
            await request(`/virtual-machines/${resourceID}/snapshots`, "POST", { description });
            setDescription("");
            await loadSnapshots();
        } catch (error) {
            onError((error as Error).message);
            await loadSnapshots();
        } finally {
            setBusy(false);
        }
    }

    async function restore(snapshot: Snapshot) {
        if (!window.confirm(`Restore ${snapshot.description}? The VM's current state will be replaced.`)) {
            return;
        }
        setBusy(true);
        try {
            await request(`/vm-snapshots/${snapshot.id}/restore`, "POST", {});
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function remove(snapshot: Snapshot) {
        if (!window.confirm(`Delete snapshot ${snapshot.description}?`)) {
            return;
        }
        setBusy(true);
        try {
            await request(`/vm-snapshots/${snapshot.id}`, "DELETE");
            await loadSnapshots();
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    return (
        <section className="vm-snapshot-panel" aria-labelledby="snapshot-heading">
            <div className="resource-tools-heading"><div><p className="eyebrow">Recovery</p><h3 id="snapshot-heading">Snapshots</h3></div></div>
            <form className="snapshot-create-form" onSubmit={createSnapshot}>
                <label>Snapshot note<input maxLength={160} value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Before system update" required /></label>
                <button className="primary-action" type="submit" disabled={busy}>{busy ? "Working…" : "Create snapshot"}</button>
            </form>
            {snapshots.length === 0 ? <p className="snapshot-empty">No managed snapshots.</p> : <ul className="snapshot-list">{snapshots.map((snapshot) => (
                <li key={snapshot.id}>
                    <div><strong>{snapshot.description}</strong><small>{snapshot.state} · reserves {snapshot.reserved_gib} GiB · {new Date(snapshot.created_at).toLocaleString()}</small></div>
                    <div className="snapshot-actions">
                        <button className="icon-button" type="button" aria-label={`Restore ${snapshot.description}`} title="Restore" onClick={() => restore(snapshot)} disabled={busy || snapshot.state !== "ready"}><RotateCcw size={15} /></button>
                        <button className="icon-button" type="button" aria-label={`Delete ${snapshot.description}`} title="Delete" onClick={() => remove(snapshot)} disabled={busy}><Trash2 size={15} /></button>
                    </div>
                </li>
            ))}</ul>}
        </section>
    );
}
