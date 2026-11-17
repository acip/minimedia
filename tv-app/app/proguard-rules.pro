# Retrofit, OkHttp, kotlinx.serialization and Media3 ship their own R8 rules.
# The API models are (de)serialized by generated serializers; keep their names
# readable in crash reports.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile
