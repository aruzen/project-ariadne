namespace Ariadne.Windows.Protocol;

internal sealed class SnapshotStore
{
    private readonly object gate = new();
    private Snapshot current = new();
    private TaskCompletionSource revisionChanged = NewRevisionSource();

    public event Action<Snapshot>? Changed;

    public Snapshot Current
    {
        get
        {
            lock (gate)
            {
                return Clone(current);
            }
        }
    }

    public void Initialize(Snapshot snapshot)
    {
        Snapshot copy;
        lock (gate)
        {
            current = Clone(snapshot);
            revisionChanged.TrySetResult();
            revisionChanged = NewRevisionSource();
            copy = Clone(current);
        }
        Changed?.Invoke(copy);
    }

    public void Apply(CoreEvent value)
    {
        Snapshot copy;
        lock (gate)
        {
            if (value.Revision != current.Revision + 1)
            {
                throw new InvalidDataException($"Core event revision {value.Revision} follows {current.Revision}");
            }

            var payload = value.Payload;
            switch (value.Kind)
            {
                case "workspace_created":
                {
                    var workspace = Required<WorkspaceModel>(payload, "workspace");
                    Upsert(current.Workspaces, workspace, x => x.Id);
                    current.NextWorkspaceId = NextAfter(current.NextWorkspaceId, workspace.Id);
                    break;
                }
                case "workspace_renamed":
                    Upsert(current.Workspaces, Required<WorkspaceModel>(payload, "workspace"), x => x.Id);
                    break;
                case "workspace_deleted":
                    Remove(current.Workspaces, Required<WorkspaceModel>(payload, "workspace").Id, x => x.Id);
                    break;
                case "window_created":
                {
                    var window = Required<WindowModel>(payload, "window");
                    Upsert(current.Windows, window, x => x.Id);
                    current.NextWindowId = NextAfter(current.NextWindowId, window.Id);
                    break;
                }
                case "window_renamed":
                case "split_resized":
                    Upsert(current.Windows, Required<WindowModel>(payload, "window"), x => x.Id);
                    break;
                case "window_deleted":
                    Remove(current.Windows, Required<WindowModel>(payload, "window").Id, x => x.Id);
                    Upsert(current.Workspaces, Required<WorkspaceModel>(payload, "workspace"), x => x.Id);
                    break;
                case "pane_created":
                {
                    var pane = Required<PaneModel>(payload, "pane");
                    var window = Required<WindowModel>(payload, "window");
                    Upsert(current.Panes, pane, x => x.Id);
                    Upsert(current.Windows, window, x => x.Id);
                    current.NextPaneId = NextAfter(current.NextPaneId, pane.Id);
                    BumpNextSplitId(current, window);
                    if (payload.TryGetProperty("tool", out var tool) && tool.ValueKind == System.Text.Json.JsonValueKind.Object)
                    {
                        UpsertTool(current.ToolInstances, Json.Deserialize<ToolInstanceModel>(System.Text.Encoding.UTF8.GetBytes(tool.GetRawText())));
                    }
                    break;
                }
                case "pane_moved":
                {
                    var pane = Required<PaneModel>(payload, "pane");
                    var source = Required<WindowModel>(payload, "source_window");
                    var destination = Required<WindowModel>(payload, "destination_window");
                    Upsert(current.Panes, pane, x => x.Id);
                    Upsert(current.Windows, source, x => x.Id);
                    Upsert(current.Windows, destination, x => x.Id);
                    BumpNextSplitId(current, source);
                    BumpNextSplitId(current, destination);
                    break;
                }
                case "pane_closed":
                {
                    var pane = Required<PaneModel>(payload, "pane");
                    Remove(current.Panes, pane.Id, x => x.Id);
                    Remove(current.StashedPanes, pane.Id, x => x.PaneId);
                    var window = Required<WindowModel>(payload, "window");
                    if (window.Id != 0)
                    {
                        Upsert(current.Windows, window, x => x.Id);
                    }
                    RemovePayloadItems<AttentionModel>(current.Attentions, payload, "removed_attentions", x => x.Id);
                    if (payload.TryGetProperty("removed_tool", out var removedTool) && removedTool.ValueKind == System.Text.Json.JsonValueKind.Object)
                    {
                        RemoveTool(current.ToolInstances,
                            Json.Deserialize<ToolInstanceModel>(System.Text.Encoding.UTF8.GetBytes(removedTool.GetRawText())).Descriptor);
                    }
                    break;
                }
                case "pane_stashed":
                case "pane_restored":
                {
                    var pane = Required<PaneModel>(payload, "pane");
                    Upsert(current.Panes, pane, x => x.Id);
                    Upsert(current.Windows, Required<WindowModel>(payload, "window"), x => x.Id);
                    if (value.Kind == "pane_stashed")
                    {
                        Upsert(current.StashedPanes, Required<StashedPaneModel>(payload, "stashed"), x => x.PaneId);
                    }
                    else
                    {
                        Remove(current.StashedPanes, pane.Id, x => x.PaneId);
                        BumpNextSplitId(current, Required<WindowModel>(payload, "window"));
                    }
                    break;
                }
                case "window_stashed":
                case "window_restored":
                {
                    var window = Required<WindowModel>(payload, "window");
                    Upsert(current.Windows, window, x => x.Id);
                    Upsert(current.Workspaces, Required<WorkspaceModel>(payload, "workspace"), x => x.Id);
                    if (value.Kind == "window_stashed")
                    {
                        Upsert(current.StashedWindows, Required<StashedWindowModel>(payload, "stashed"), x => x.WindowId);
                    }
                    else
                    {
                        Remove(current.StashedWindows, window.Id, x => x.WindowId);
                    }
                    break;
                }
                case "terminal_exited":
                case "terminal_unavailable":
                case "terminal_started":
                case "terminal_start_failed":
                case "terminal_restarting":
                case "terminal_run_prepared":
                case "terminal_stopping":
                    Upsert(current.Panes, Required<PaneModel>(payload, "pane"), x => x.Id);
                    break;
                case "label_set":
                case "label_removed":
                case "label_source_cleared":
                case "tool_state_updated":
                    UpsertTool(current.ToolInstances, Required<ToolInstanceModel>(payload, "tool"));
                    break;
                case "attention_raised":
                case "attention_acknowledged":
                    Upsert(current.Attentions, Required<AttentionModel>(payload, "attention"), x => x.Id);
                    RemovePayloadItems<AttentionModel>(current.Attentions, payload, "removed", x => x.Id);
                    break;
                case "attention_source_cleared":
                    RemovePayloadItems<AttentionModel>(current.Attentions, payload, "attentions", x => x.Id);
                    break;
                default:
                    throw new InvalidDataException($"unsupported Core event {value.Kind}");
            }

            current.Revision = value.Revision;
            revisionChanged.TrySetResult();
            revisionChanged = NewRevisionSource();
            copy = Clone(current);
        }
        Changed?.Invoke(copy);
    }

    public async Task WaitForRevisionAsync(ulong revision, CancellationToken cancellationToken)
    {
        while (true)
        {
            Task wait;
            lock (gate)
            {
                if (current.Revision >= revision)
                {
                    return;
                }
                wait = revisionChanged.Task;
            }
            await wait.WaitAsync(cancellationToken).ConfigureAwait(false);
        }
    }

    private static T Required<T>(System.Text.Json.JsonElement payload, string name)
    {
        if (!payload.TryGetProperty(name, out var value))
        {
            throw new InvalidDataException($"Core event lacks {name}");
        }
        return Json.Deserialize<T>(System.Text.Encoding.UTF8.GetBytes(value.GetRawText()));
    }

    private static void Upsert<T>(List<T> values, T value, Func<T, ulong> id)
    {
        var index = values.FindIndex(candidate => id(candidate) == id(value));
        if (index < 0)
        {
            values.Add(value);
        }
        else
        {
            values[index] = value;
        }
    }

    private static void Remove<T>(List<T> values, ulong value, Func<T, ulong> id) =>
        values.RemoveAll(candidate => id(candidate) == value);

    private static void RemovePayloadItems<T>(List<T> values, System.Text.Json.JsonElement payload, string name, Func<T, ulong> id)
    {
        if (!payload.TryGetProperty(name, out var items) || items.ValueKind != System.Text.Json.JsonValueKind.Array)
        {
            return;
        }
        foreach (var item in items.EnumerateArray())
        {
            var value = Json.Deserialize<T>(System.Text.Encoding.UTF8.GetBytes(item.GetRawText()));
            Remove(values, id(value), id);
        }
    }

    private static ulong NextAfter(ulong currentValue, ulong allocated) =>
        allocated == ulong.MaxValue ? currentValue : Math.Max(currentValue, allocated + 1);

    private static void BumpNextSplitId(Snapshot snapshot, WindowModel window)
    {
        if (window.Layout is null)
        {
            return;
        }
        var maximum = MaximumSplitId(window.Layout);
        snapshot.NextSplitId = NextAfter(snapshot.NextSplitId, maximum);
    }

    private static ulong MaximumSplitId(LayoutNode node)
    {
        var maximum = node.SplitId;
        foreach (var child in node.Children)
        {
            maximum = Math.Max(maximum, MaximumSplitId(child));
        }
        return maximum;
    }

    private static void UpsertTool(List<ToolInstanceModel> tools, ToolInstanceModel tool)
    {
        var index = tools.FindIndex(value => SameTool(value.Descriptor, tool.Descriptor));
        if (index < 0)
        {
            tools.Add(tool);
        }
        else
        {
            tools[index] = tool;
        }
    }

    private static void RemoveTool(List<ToolInstanceModel> tools, ToolDescriptor descriptor) =>
        tools.RemoveAll(value => SameTool(value.Descriptor, descriptor));

    private static bool SameTool(ToolDescriptor left, ToolDescriptor right) =>
        left.Provider == right.Provider && left.Type == right.Type && left.Instance == right.Instance;

    private static Snapshot Clone(Snapshot value) => Json.Deserialize<Snapshot>(Json.Serialize(value));

    private static TaskCompletionSource NewRevisionSource() =>
        new(TaskCreationOptions.RunContinuationsAsynchronously);
}
