using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ariadne.Windows.Protocol;

internal static class Json
{
    public static readonly JsonSerializerOptions Options = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        PropertyNameCaseInsensitive = false,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    public static byte[] Serialize<T>(T value) => JsonSerializer.SerializeToUtf8Bytes(value, Options);

    public static T Deserialize<T>(ReadOnlySpan<byte> value) =>
        JsonSerializer.Deserialize<T>(value, Options) ?? throw new InvalidDataException("empty JSON value");
}
