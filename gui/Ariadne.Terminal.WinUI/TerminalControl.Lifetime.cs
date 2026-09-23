#nullable enable
namespace Microsoft.Terminal.Wpf;

public partial class TerminalControl : IDisposable
{
    private int ariadneDisposed;

    public void Dispose()
    {
        if (Interlocked.Exchange(ref ariadneDisposed, 1) != 0) return;
        Connection = null!;
        termContainer.Dispose();
    }
}
