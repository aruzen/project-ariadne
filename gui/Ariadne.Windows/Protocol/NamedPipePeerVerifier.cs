using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Principal;
using Microsoft.Win32.SafeHandles;

namespace Ariadne.Windows.Protocol;

internal static class NamedPipePeerVerifier
{
    private const uint ProcessQueryLimitedInformation = 0x1000;
    private const uint TokenQuery = 0x0008;
    private const int TokenUser = 1;

    public static void VerifyCurrentUserServer(SafePipeHandle pipe)
    {
        if (!GetNamedPipeServerProcessId(pipe, out var processId) || processId == 0)
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "cannot identify Ariadne pipe server");
        }
        using var process = OpenProcess(ProcessQueryLimitedInformation, false, processId);
        if (process.IsInvalid)
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "cannot open Ariadne server process");
        }
        if (!OpenProcessToken(process, TokenQuery, out var token))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "cannot inspect Ariadne server token");
        }
        using (token)
        {
            _ = GetTokenInformation(token, TokenUser, IntPtr.Zero, 0, out var length);
            if (length == 0)
            {
                throw new Win32Exception(Marshal.GetLastWin32Error(), "cannot size Ariadne server token");
            }
            var buffer = Marshal.AllocHGlobal((int)length);
            try
            {
                if (!GetTokenInformation(token, TokenUser, buffer, length, out _))
                {
                    throw new Win32Exception(Marshal.GetLastWin32Error(), "cannot read Ariadne server token");
                }
                var serverSid = new SecurityIdentifier(Marshal.ReadIntPtr(buffer)).Value;
                using var current = WindowsIdentity.GetCurrent();
                if (!string.Equals(serverSid, current.User?.Value, StringComparison.OrdinalIgnoreCase))
                {
                    throw new UnauthorizedAccessException("Ariadne pipe server belongs to a different Windows user");
                }
            }
            finally
            {
                Marshal.FreeHGlobal(buffer);
            }
        }
    }

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetNamedPipeServerProcessId(SafePipeHandle pipe, out uint serverProcessId);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern SafeProcessHandle OpenProcess(uint desiredAccess, [MarshalAs(UnmanagedType.Bool)] bool inheritHandle, uint processId);

    [DllImport("advapi32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool OpenProcessToken(SafeProcessHandle process, uint desiredAccess, out SafeAccessTokenHandle token);

    [DllImport("advapi32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetTokenInformation(
        SafeAccessTokenHandle token,
        int tokenInformationClass,
        IntPtr tokenInformation,
        uint tokenInformationLength,
        out uint returnLength);
}
