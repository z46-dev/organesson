import { useEffect, useRef, useState } from "react";
import { Plus, RotateCcw, Trash2, X } from "lucide-react";
import type { FormEvent } from "react";
import type { ApiRequest } from "./api";

type Snapshot = { id: number; name: string; description: string; state: string; created_at: string };
type SnapshotQuota = { vm_snapshots: number; vm_max_snapshots: number; validated: boolean };
type Props = { resourceID: number; request: ApiRequest; onError: (message: string) => void };

// Provides the managed snapshot lifecycle and platform capacity for one VM.
export function VMSnapshotPanel({ resourceID, request, onError }: Props) {
    const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
    const [quota, setQuota] = useState<SnapshotQuota | null>(null);
    const [description, setDescription] = useState("");
    const [createOpen, setCreateOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    const dialogRef = useRef<HTMLDialogElement>(null);

    async function loadSnapshots() {
        try {
            const result = await request<{ snapshots: Snapshot[]; snapshot_quota?: SnapshotQuota }>(`/virtual-machines/${resourceID}/snapshots`);
            setSnapshots(result.snapshots ?? []);
            setQuota(result.snapshot_quota ?? null);
        } catch (error) {
            onError((error as Error).message);
        }
    }

    useEffect(() => {
        loadSnapshots();
    }, [resourceID]);

    useEffect(() => {
        const dialog = dialogRef.current;
        if (createOpen && dialog && !dialog.open) {
            dialog.showModal();
        } else if (!createOpen && dialog?.open) {
            dialog.close();
        }
    }, [createOpen]);

    async function createSnapshot(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        try {
            await request(`/virtual-machines/${resourceID}/snapshots`, "POST", { description });
            setDescription("");
            setCreateOpen(false);
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
            await loadSnapshots();
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

    const snapshotFits = quota?.validated === true && (quota.vm_max_snapshots === 0 || quota.vm_snapshots < quota.vm_max_snapshots);

    return (
        <section className="vm-snapshot-panel" aria-labelledby="snapshot-heading">
            <header className="snapshot-heading">
                <h4 id="snapshot-heading">Snapshots</h4>
                {quota?.vm_max_snapshots ? <span className="snapshot-quota-count">{quota.vm_snapshots} / {quota.vm_max_snapshots}</span> : null}
                <button className="icon-button snapshot-add-button" type="button" aria-label="Create snapshot" onClick={() => setCreateOpen(true)} disabled={busy || !snapshotFits}><Plus size={17} /></button>
            </header>
            {snapshots.length === 0 ? <p className="snapshot-empty">No managed snapshots.</p> : <ul className="snapshot-list">{snapshots.map((snapshot) => (
                <li key={snapshot.id}>
                    <div className="snapshot-summary">
                        <div className="snapshot-title"><strong>{snapshot.description}</strong></div>
                        <small>{new Date(snapshot.created_at).toLocaleString()}</small>
                    </div>
                    <div className="snapshot-actions">
                        <button className="icon-button" type="button" aria-label={`Restore ${snapshot.description}`} title="Restore" onClick={() => restore(snapshot)} disabled={busy || snapshot.state !== "ready"}><RotateCcw size={15} /></button>
                        <button className="icon-button" type="button" aria-label={`Delete ${snapshot.description}`} title="Delete" onClick={() => remove(snapshot)} disabled={busy}><Trash2 size={15} /></button>
                    </div>
                </li>
            ))}</ul>}
            <dialog className="snapshot-dialog" ref={dialogRef} onClose={() => setCreateOpen(false)} onClick={(event) => {
                if (event.target === event.currentTarget) {
                    setCreateOpen(false);
                }
            }}>
                <form onSubmit={createSnapshot}>
                    <header><h3>Create snapshot</h3><button className="icon-button" type="button" aria-label="Close" onClick={() => setCreateOpen(false)} disabled={busy}><X size={16} /></button></header>
                    <label>Snapshot note<input autoFocus maxLength={160} value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Before system update" required /></label>
                    <footer><button className="quiet-button" type="button" onClick={() => setCreateOpen(false)} disabled={busy}>Cancel</button><button className="primary-action" type="submit" disabled={busy || !description.trim()}>{busy ? "Creating…" : "Create snapshot"}</button></footer>
                </form>
            </dialog>
        </section>
    );
}
