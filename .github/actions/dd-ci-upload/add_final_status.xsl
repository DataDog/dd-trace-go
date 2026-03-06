<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">

  <!-- Identity transform: copy everything as-is by default -->
  <xsl:template match="@*|node()">
    <xsl:copy>
      <xsl:apply-templates select="@*|node()"/>
    </xsl:copy>
  </xsl:template>

  <!-- For testcase elements missing dd_tags[test.final_status] inside their properties block -->
  <xsl:template match="testcase[not(properties/property[@name='dd_tags[test.final_status]'])]">
    <xsl:copy>
      <xsl:apply-templates select="@*"/>
      <xsl:variable name="status">
        <xsl:choose>
          <xsl:when test="failure or error">fail</xsl:when>
          <xsl:when test="skipped">skip</xsl:when>
          <xsl:otherwise>pass</xsl:otherwise>
        </xsl:choose>
      </xsl:variable>
      <xsl:choose>
        <xsl:when test="properties">
          <!-- Inject into existing properties block, preserving child order -->
          <xsl:for-each select="node()">
            <xsl:choose>
              <xsl:when test="self::properties">
                <properties>
                  <xsl:apply-templates select="@*|node()"/>
                  <property name="dd_tags[test.final_status]" value="{$status}"/>
                </properties>
              </xsl:when>
              <xsl:otherwise>
                <xsl:apply-templates select="."/>
              </xsl:otherwise>
            </xsl:choose>
          </xsl:for-each>
        </xsl:when>
        <xsl:otherwise>
          <!-- No properties block: create one before other children -->
          <properties>
            <property name="dd_tags[test.final_status]" value="{$status}"/>
          </properties>
          <xsl:apply-templates select="node()"/>
        </xsl:otherwise>
      </xsl:choose>
    </xsl:copy>
  </xsl:template>

</xsl:stylesheet>
