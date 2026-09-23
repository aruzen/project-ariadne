using System.Collections;
using Ariadne.Windows.Protocol;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Ariadne.WinUI;

public sealed partial class MainWindow
{
    private readonly SemaphoreSlim interactionGate = new(1, 1);

    private async Task<PluginInteractionResult> HandlePluginInteractionAsync(
        PluginInteractionRequest request,
        CancellationToken cancellationToken)
    {
        await interactionGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (request.Interaction.Kind == "editor")
                return await HandlePluginEditorAsync(request, cancellationToken).ConfigureAwait(false);
            return await RunOnUIAsync(async () =>
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (request.Interaction.Kind == "confirm")
                {
                    var dialog = new ContentDialog
                    {
                        XamlRoot = Content.XamlRoot,
                        Title = request.Context.PluginId,
                        Content = request.Interaction.Message,
                        PrimaryButtonText = "Yes",
                        CloseButtonText = "No",
                        DefaultButton = ContentDialogButton.Close,
                    };
                    return new PluginInteractionResult
                    {
                        Confirmed = await dialog.ShowAsync() == ContentDialogResult.Primary,
                    };
                }
                if (request.Interaction.Kind != "prompt")
                    throw new InvalidOperationException($"unsupported plugin interaction {request.Interaction.Kind}");
                var input = new TextBox
                {
                    Text = request.Interaction.Text,
                    Header = request.Interaction.Message,
                    MinWidth = 480,
                };
                var prompt = new ContentDialog
                {
                    XamlRoot = Content.XamlRoot,
                    Title = request.Context.PluginId,
                    Content = input,
                    PrimaryButtonText = "OK",
                    CloseButtonText = "Cancel",
                    DefaultButton = ContentDialogButton.Primary,
                };
                if (await prompt.ShowAsync() != ContentDialogResult.Primary)
                    throw new OperationCanceledException(cancellationToken);
                return new PluginInteractionResult { Text = input.Text };
            }).ConfigureAwait(false);
        }
        finally
        {
            interactionGate.Release();
        }
    }

    private async Task<PluginInteractionResult> HandlePluginEditorAsync(
        PluginInteractionRequest request,
        CancellationToken cancellationToken)
    {
        var currentClient = client ?? throw new InvalidOperationException("frontend is disconnected");
        var editor = currentClient.Gui.Editor.Count == 0 ? new List<string> { "notepad.exe" } : currentClient.Gui.Editor;
        var environment = Environment.GetEnvironmentVariables().Cast<DictionaryEntry>()
            .Select(value => $"{value.Key}={value.Value}").ToList();
        PluginEditorResult? opened = null;
        var previousPreview = previewPaneId;
        var previousPane = paneId;
        var previousZoom = zoomPaneId;
        try
        {
            var result = await currentClient.PluginAsync(new PluginManageRequest
            {
                Action = "editor.open",
                Editor = new PluginEditorRequest
                {
                    Argv = editor,
                    Cwd = Environment.CurrentDirectory,
                    Env = environment,
                    Text = request.Interaction.Text,
                },
            }, cancellationToken).ConfigureAwait(false);
            opened = result.Editor ?? throw new InvalidDataException("editor.open returned no editor");

            var exited = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            void Observe(Snapshot value)
            {
                var pane = value.Panes.FirstOrDefault(candidate => candidate.Id == opened.PaneId);
                if (pane?.Terminal?.State is "exited" or "failed")
                    exited.TrySetResult(pane.Terminal.Exit is { Kind: "process", Code: 0 });
            }
            currentClient.Store.Changed += Observe;
            using var registration = cancellationToken.Register(() => exited.TrySetCanceled(cancellationToken));
            try
            {
                await RunOnUIAsync(() =>
                {
                    previewPaneId = opened.PaneId;
                    paneId = opened.PaneId;
                    zoomPaneId = 0;
                    Refresh();
                    return Task.FromResult(true);
                }).ConfigureAwait(false);
                Observe(currentClient.Store.Current);
                if (!await exited.Task.ConfigureAwait(false))
                    throw new OperationCanceledException("editor exited unsuccessfully", cancellationToken);
            }
            finally
            {
                currentClient.Store.Changed -= Observe;
            }

            var finished = await currentClient.PluginAsync(new PluginManageRequest
            {
                Action = "editor.finish",
                PaneId = opened.PaneId,
            }, cancellationToken).ConfigureAwait(false);
            return new PluginInteractionResult { Text = finished.Editor?.Text ?? "" };
        }
        catch
        {
            if (opened is not null)
            {
                try
                {
                    using var cleanup = new CancellationTokenSource(TimeSpan.FromSeconds(5));
                    await currentClient.PluginAsync(new PluginManageRequest
                    {
                        Action = "editor.cancel",
                        PaneId = opened.PaneId,
                    }, cleanup.Token).ConfigureAwait(false);
                }
                catch
                {
                }
            }
            throw;
        }
        finally
        {
            await RunOnUIAsync(() =>
            {
                previewPaneId = previousPreview;
                paneId = previousPane;
                zoomPaneId = previousZoom;
                Refresh();
                return Task.FromResult(true);
            }).ConfigureAwait(false);
        }
    }

    private Task<T> RunOnUIAsync<T>(Func<Task<T>> action)
    {
        if (DispatcherQueue.HasThreadAccess) return action();
        var completion = new TaskCompletionSource<T>(TaskCreationOptions.RunContinuationsAsynchronously);
        if (!DispatcherQueue.TryEnqueue(async () =>
            {
                try
                {
                    completion.TrySetResult(await action());
                }
                catch (Exception error)
                {
                    completion.TrySetException(error);
                }
            }))
            completion.TrySetException(new InvalidOperationException("frontend dispatcher is shutting down"));
        return completion.Task;
    }
}
